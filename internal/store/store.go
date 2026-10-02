// Package store owns the SQLite file and every write the service performs.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("promotion not found")

// Rule is one order_threshold rule belonging to a promotion.
type Rule struct {
	ID                       string   `json:"id"`
	Kind                     string   `json:"kind"`
	MinOrderAmountCents      int64    `json:"min_order_amount_cents"`
	DiscountAmountCents      int64    `json:"discount_amount_cents"`
	Stackable                bool     `json:"stackable"`
	MutuallyExclusiveRuleIDs []string `json:"mutually_exclusive_rule_ids"`
}

// Promotion is the persisted activity definition returned by the API.
type Promotion struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Rules            []Rule `json:"rules"`
	BudgetTotalCents int64  `json:"budget_total_cents"`
	BudgetUsedCents  int64  `json:"budget_used_cents"`
	Status           string `json:"status"`
}

// NewPromotionInput carries the validated request fields used to create a promotion.
type NewPromotionInput struct {
	Name             string
	Rules            []Rule
	BudgetTotalCents int64
}

// Store wraps the SQLite handle so callers never touch database/sql directly.
type Store struct {
	db *sql.DB
}

// Open prepares the database file and the schema this service needs.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable wal: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Ping reports whether the storage layer is usable.
func (s *Store) Ping() error { return s.db.Ping() }

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS service_metadata (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS promotions (
	id                 TEXT PRIMARY KEY,
	name               TEXT NOT NULL,
	budget_total_cents INTEGER NOT NULL,
	budget_used_cents  INTEGER NOT NULL DEFAULT 0,
	status             TEXT NOT NULL DEFAULT 'draft'
);

CREATE TABLE IF NOT EXISTS promotion_rules (
	promotion_id           TEXT NOT NULL,
	position               INTEGER NOT NULL,
	rule_id                TEXT NOT NULL,
	kind                   TEXT NOT NULL,
	min_order_amount_cents INTEGER NOT NULL,
	discount_amount_cents  INTEGER NOT NULL,
	stackable              INTEGER NOT NULL,
	mutually_exclusive_ids TEXT NOT NULL,
	PRIMARY KEY (promotion_id, rule_id),
	FOREIGN KEY (promotion_id) REFERENCES promotions(id)
);
`

// CreatePromotion persists a new promotion with all of its rules in one transaction.
// The generated promotion id is returned on the populated promotion.
func (s *Store) CreatePromotion(ctx context.Context, input NewPromotionInput) (*Promotion, error) {
	promotion := &Promotion{
		ID:               uuid.NewString(),
		Name:             input.Name,
		Rules:            input.Rules,
		BudgetTotalCents: input.BudgetTotalCents,
		BudgetUsedCents:  0,
		Status:           "draft",
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO promotions (id, name, budget_total_cents, budget_used_cents, status) VALUES (?, ?, ?, 0, 'draft')`,
		promotion.ID, promotion.Name, promotion.BudgetTotalCents); err != nil {
		return nil, fmt.Errorf("insert promotion: %w", err)
	}

	for position, rule := range promotion.Rules {
		exclusiveIDs, err := json.Marshal(rule.MutuallyExclusiveRuleIDs)
		if err != nil {
			return nil, fmt.Errorf("encode exclusive ids: %w", err)
		}
		stackable := 0
		if rule.Stackable {
			stackable = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO promotion_rules (promotion_id, position, rule_id, kind, min_order_amount_cents, discount_amount_cents, stackable, mutually_exclusive_ids)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			promotion.ID, position, rule.ID, rule.Kind, rule.MinOrderAmountCents, rule.DiscountAmountCents, stackable, string(exclusiveIDs)); err != nil {
			return nil, fmt.Errorf("insert rule: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return promotion, nil
}

// GetPromotion loads a promotion and its rules by id. ErrNotFound is returned when no such
// promotion exists. Reads do not mutate the stored promotion.
func (s *Store) GetPromotion(ctx context.Context, id string) (*Promotion, error) {
	promotion := &Promotion{Rules: []Rule{}}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, budget_total_cents, budget_used_cents, status FROM promotions WHERE id = ?`, id).
		Scan(&promotion.ID, &promotion.Name, &promotion.BudgetTotalCents, &promotion.BudgetUsedCents, &promotion.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select promotion: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT rule_id, kind, min_order_amount_cents, discount_amount_cents, stackable, mutually_exclusive_ids
		 FROM promotion_rules WHERE promotion_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, fmt.Errorf("select rules: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rule Rule
		var stackable int
		var exclusiveIDsJSON string
		if err := rows.Scan(&rule.ID, &rule.Kind, &rule.MinOrderAmountCents, &rule.DiscountAmountCents, &stackable, &exclusiveIDsJSON); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		rule.Stackable = stackable != 0
		if err := json.Unmarshal([]byte(exclusiveIDsJSON), &rule.MutuallyExclusiveRuleIDs); err != nil {
			return nil, fmt.Errorf("decode exclusive ids: %w", err)
		}
		promotion.Rules = append(promotion.Rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rules: %w", err)
	}
	return promotion, nil
}
