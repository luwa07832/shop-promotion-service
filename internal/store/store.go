// Package store owns the SQLite file and every write the service performs.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// ErrNotFound signals that no promotion matches the given identifier.
var ErrNotFound = errors.New("promotion not found")

// Rule is one promotion rule inside a promotion.
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

// CreatePromotion assigns a server-generated id and persists the promotion and
// its rules in one transaction.
func (s *Store) CreatePromotion(promotion *Promotion) error {
	if promotion.ID == "" {
		promotion.ID = uuid.NewString()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin create promotion: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO promotions (id, name, budget_total_cents, budget_used_cents, status)
		 VALUES (?, ?, ?, ?, ?)`,
		promotion.ID,
		promotion.Name,
		promotion.BudgetTotalCents,
		promotion.BudgetUsedCents,
		promotion.Status,
	); err != nil {
		return fmt.Errorf("insert promotion: %w", err)
	}

	for position, rule := range promotion.Rules {
		exclusiveIDs := rule.MutuallyExclusiveRuleIDs
		if exclusiveIDs == nil {
			exclusiveIDs = []string{}
		}
		rawExclusiveIDs, err := json.Marshal(exclusiveIDs)
		if err != nil {
			return fmt.Errorf("encode exclusive rule ids: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO promotion_rules
			    (promotion_id, position, id, kind, min_order_amount_cents, discount_amount_cents, stackable, mutually_exclusive_rule_ids)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			promotion.ID,
			position,
			rule.ID,
			rule.Kind,
			rule.MinOrderAmountCents,
			rule.DiscountAmountCents,
			rule.Stackable,
			string(rawExclusiveIDs),
		); err != nil {
			return fmt.Errorf("insert promotion rule: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create promotion: %w", err)
	}
	return nil
}

// GetPromotion returns the promotion with the given id together with its rules
// in request order. It returns ErrNotFound when no such promotion exists.
func (s *Store) GetPromotion(id string) (*Promotion, error) {
	promotion := &Promotion{}
	err := s.db.QueryRow(
		`SELECT id, name, budget_total_cents, budget_used_cents, status
		   FROM promotions WHERE id = ?`,
		id,
	).Scan(
		&promotion.ID,
		&promotion.Name,
		&promotion.BudgetTotalCents,
		&promotion.BudgetUsedCents,
		&promotion.Status,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load promotion: %w", err)
	}

	rows, err := s.db.Query(
		`SELECT id, kind, min_order_amount_cents, discount_amount_cents, stackable, mutually_exclusive_rule_ids
		   FROM promotion_rules WHERE promotion_id = ? ORDER BY position`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("load promotion rules: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var rule Rule
		var stackable int
		var rawExclusiveIDs string
		if err := rows.Scan(
			&rule.ID,
			&rule.Kind,
			&rule.MinOrderAmountCents,
			&rule.DiscountAmountCents,
			&stackable,
			&rawExclusiveIDs,
		); err != nil {
			return nil, fmt.Errorf("scan promotion rule: %w", err)
		}
		rule.Stackable = stackable != 0
		if err := json.Unmarshal([]byte(rawExclusiveIDs), &rule.MutuallyExclusiveRuleIDs); err != nil {
			return nil, fmt.Errorf("decode exclusive rule ids: %w", err)
		}
		if rule.MutuallyExclusiveRuleIDs == nil {
			rule.MutuallyExclusiveRuleIDs = []string{}
		}
		promotion.Rules = append(promotion.Rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate promotion rules: %w", err)
	}
	if promotion.Rules == nil {
		promotion.Rules = []Rule{}
	}
	return promotion, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS service_metadata (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS promotions (
	id                 TEXT PRIMARY KEY,
	name               TEXT NOT NULL,
	budget_total_cents INTEGER NOT NULL,
	budget_used_cents  INTEGER NOT NULL,
	status             TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS promotion_rules (
	promotion_id                TEXT NOT NULL,
	position                    INTEGER NOT NULL,
	id                          TEXT NOT NULL,
	kind                        TEXT NOT NULL,
	min_order_amount_cents      INTEGER NOT NULL,
	discount_amount_cents       INTEGER NOT NULL,
	stackable                   INTEGER NOT NULL,
	mutually_exclusive_rule_ids TEXT NOT NULL,
	PRIMARY KEY (promotion_id, position)
);
`
