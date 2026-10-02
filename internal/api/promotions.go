package api

import (

	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/luwa07832/shop-promotion-service/internal/store"
)

const maxPromotionBodyBytes = 1 << 20

const (
	ruleKindOrderThreshold = "order_threshold"
	promotionStatusDraft   = "draft"
)

type ruleInput struct {
	ID                       *string   `json:"id"`
	Kind                     *string   `json:"kind"`
	MinOrderAmountCents      *int64    `json:"min_order_amount_cents"`
	DiscountAmountCents      *int64    `json:"discount_amount_cents"`
	Stackable                *bool     `json:"stackable"`
	MutuallyExclusiveRuleIDs *[]string `json:"mutually_exclusive_rule_ids"`
}

type promotionInput struct {
	Name             *string      `json:"name"`
	Rules            *[]ruleInput `json:"rules"`
	BudgetTotalCents *int64       `json:"budget_total_cents"`
}

// PromotionAPI serves the promotion definition endpoints.
type PromotionAPI struct {
	store *store.Store
}

// NewPromotionAPI binds the promotion endpoints to the given store.
func NewPromotionAPI(st *store.Store) *PromotionAPI {
	return &PromotionAPI{store: st}
}

// Register adds the promotion routes to the router.
func (p *PromotionAPI) Register(router *gin.Engine) {
	router.POST("/v1/promotions", p.create)
	router.GET("/v1/promotions/:id", p.get)
}

func (p *PromotionAPI) create(c *gin.Context) {
	var input promotionInput
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxPromotionBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		respondInvalidInput(c, decodeErrorMessage(err))
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		respondInvalidInput(c, "request body must contain a single JSON object")
		return
	}

	promotion, err := buildPromotion(input)
	if err != nil {
		respondInvalidInput(c, err.Error())
		return
	}

	if err := p.store.CreatePromotion(promotion); err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"promotion": promotion})
}

func (p *PromotionAPI) get(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		respondPromotionNotFound(c)
		return
	}

	promotion, err := p.store.GetPromotion(id)
	if errors.Is(err, store.ErrNotFound) {
		respondPromotionNotFound(c)
		return
	}
	if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"promotion": promotion})
}

func buildPromotion(input promotionInput) (*store.Promotion, error) {
	if input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		return nil, errors.New("name must be a non-empty string")
	}
	if input.BudgetTotalCents == nil {
		return nil, errors.New("budget_total_cents is required")
	}
	if *input.BudgetTotalCents <= 0 {
		return nil, errors.New("budget_total_cents must be greater than 0")
	}
	if input.Rules == nil {
		return nil, errors.New("rules is required")
	}
	if len(*input.Rules) == 0 {
		return nil, errors.New("rules must be a non-empty array")
	}

	rules := make([]store.Rule, 0, len(*input.Rules))
	ruleIDs := make(map[string]struct{}, len(*input.Rules))
	for index, rule := range *input.Rules {
		label := fmt.Sprintf("rules[%d]", index)

		if rule.ID == nil || strings.TrimSpace(*rule.ID) == "" {
			return nil, fmt.Errorf("%s.id must be a non-empty string", label)
		}
		id := *rule.ID
		if _, exists := ruleIDs[id]; exists {
			return nil, fmt.Errorf("%s.id duplicates another rule id", label)
		}
		ruleIDs[id] = struct{}{}

		if rule.Kind == nil {
			return nil, fmt.Errorf("%s.kind is required", label)
		}
		if *rule.Kind != ruleKindOrderThreshold {
			return nil, fmt.Errorf("%s.kind must be %q", label, ruleKindOrderThreshold)
		}
		if rule.MinOrderAmountCents == nil {
			return nil, fmt.Errorf("%s.min_order_amount_cents is required", label)
		}
		if *rule.MinOrderAmountCents < 0 {
			return nil, fmt.Errorf("%s.min_order_amount_cents must be a non-negative integer", label)
		}
		if rule.DiscountAmountCents == nil {
			return nil, fmt.Errorf("%s.discount_amount_cents is required", label)
		}
		if *rule.DiscountAmountCents < 0 {
			return nil, fmt.Errorf("%s.discount_amount_cents must be a non-negative integer", label)
		}
		if *rule.DiscountAmountCents > *rule.MinOrderAmountCents {
			return nil, fmt.Errorf("%s.discount_amount_cents must not exceed min_order_amount_cents", label)
		}
		if rule.Stackable == nil {
			return nil, fmt.Errorf("%s.stackable is required", label)
		}
		if rule.MutuallyExclusiveRuleIDs == nil {
			return nil, fmt.Errorf("%s.mutually_exclusive_rule_ids is required", label)
		}

		rules = append(rules, store.Rule{
			ID:                       id,
			Kind:                     *rule.Kind,
			MinOrderAmountCents:      *rule.MinOrderAmountCents,
			DiscountAmountCents:      *rule.DiscountAmountCents,
			Stackable:                *rule.Stackable,
			MutuallyExclusiveRuleIDs: append([]string{}, (*rule.MutuallyExclusiveRuleIDs)...),
		})
	}

	for index, rule := range *input.Rules {
		for _, referencedID := range *rule.MutuallyExclusiveRuleIDs {
			if referencedID == *rule.ID {
				return nil, fmt.Errorf("rules[%d].mutually_exclusive_rule_ids must not reference the rule itself", index)
			}
			if _, exists := ruleIDs[referencedID]; !exists {
				return nil, fmt.Errorf("rules[%d].mutually_exclusive_rule_ids references an unknown rule id", index)
			}
		}
	}

	return &store.Promotion{
		Name:             *input.Name,
		Rules:            rules,
		BudgetTotalCents: *input.BudgetTotalCents,
		BudgetUsedCents:  0,
		Status:           promotionStatusDraft,
	}, nil
}

func decodeErrorMessage(err error) string {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return "request body is too large"
	}
	return fmt.Sprintf("request body must be valid JSON: %s", err.Error())
}

func respondInvalidInput(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, errorBody("invalid_promotion_input", message))
}

func respondPromotionNotFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, errorBody("promotion_not_found", "promotion not found"))
}

func respondInternalError(c *gin.Context) {
	c.JSON(http.StatusInternalServerError, errorBody("internal_error", "the service failed to handle the request"))
}

func errorBody(code, message string) gin.H {
	return gin.H{"error": gin.H{"code": code, "message": message}}
}

