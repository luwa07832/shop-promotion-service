package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/luwa07832/shop-promotion-service/internal/store"
)

const ruleKindOrderThreshold = "order_threshold"

type ruleInput struct {
	ID                       *string   `json:"id"`
	Kind                     *string   `json:"kind"`
	MinOrderAmountCents      *int64    `json:"min_order_amount_cents"`
	DiscountAmountCents      *int64    `json:"discount_amount_cents"`
	Stackable                *bool     `json:"stackable"`
	MutuallyExclusiveRuleIDs *[]string `json:"mutually_exclusive_rule_ids"`
}

type createPromotionRequest struct {
	Name             *string      `json:"name"`
	Rules            *[]ruleInput `json:"rules"`
	BudgetTotalCents *int64       `json:"budget_total_cents"`
}

func writeAPIError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

func createPromotion(st *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_promotion_input", "request body could not be read")
			return
		}

		var request createPromotionRequest
		decoder := json.NewDecoder(bytes.NewReader(body))
		if err := decoder.Decode(&request); err != nil || decoder.More() {
			writeAPIError(c, http.StatusBadRequest, "invalid_promotion_input", "request body must be a valid JSON promotion object")
			return
		}
		var trailing json.RawMessage
		if err := decoder.Decode(&trailing); err != io.EOF {
			writeAPIError(c, http.StatusBadRequest, "invalid_promotion_input", "request body must be a single JSON promotion object")
			return
		}

		input, validationErr := validatePromotionRequest(request)
		if validationErr != "" {
			writeAPIError(c, http.StatusBadRequest, "invalid_promotion_input", validationErr)
			return
		}

		promotion, err := st.CreatePromotion(c.Request.Context(), input)
		if err != nil {
			log.Printf("create promotion: %v", err)
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "the promotion could not be created")
			return
		}
		c.JSON(http.StatusCreated, gin.H{"promotion": promotion})
	}
}

func validatePromotionRequest(request createPromotionRequest) (store.NewPromotionInput, string) {
	if request.Name == nil || strings.TrimSpace(*request.Name) == "" {
		return store.NewPromotionInput{}, "name must be a non-empty string"
	}
	if request.BudgetTotalCents == nil {
		return store.NewPromotionInput{}, "budget_total_cents is required and must be a non-negative integer"
	}
	if *request.BudgetTotalCents <= 0 {
		return store.NewPromotionInput{}, "budget_total_cents must be greater than 0"
	}
	if request.Rules == nil || len(*request.Rules) == 0 {
		return store.NewPromotionInput{}, "rules must be a non-empty array"
	}

	requestedRules := *request.Rules
	rules := make([]store.Rule, 0, len(requestedRules))
	seenIDs := make(map[string]struct{}, len(requestedRules))
	for _, requested := range requestedRules {
		if requested.ID == nil || strings.TrimSpace(*requested.ID) == "" {
			return store.NewPromotionInput{}, "each rule requires a non-empty id"
		}
		ruleID := *requested.ID
		if _, exists := seenIDs[ruleID]; exists {
			return store.NewPromotionInput{}, "rule ids must be unique within the promotion"
		}
		seenIDs[ruleID] = struct{}{}

		if requested.Kind == nil || *requested.Kind != ruleKindOrderThreshold {
			return store.NewPromotionInput{}, "rule kind must be order_threshold"
		}
		if requested.MinOrderAmountCents == nil || *requested.MinOrderAmountCents < 0 {
			return store.NewPromotionInput{}, "min_order_amount_cents is required and must be a non-negative integer"
		}
		if requested.DiscountAmountCents == nil || *requested.DiscountAmountCents < 0 {
			return store.NewPromotionInput{}, "discount_amount_cents is required and must be a non-negative integer"
		}
		if *requested.DiscountAmountCents > *requested.MinOrderAmountCents {
			return store.NewPromotionInput{}, "discount_amount_cents cannot exceed min_order_amount_cents"
		}
		if requested.Stackable == nil {
			return store.NewPromotionInput{}, "stackable is required and must be a boolean"
		}
		if requested.MutuallyExclusiveRuleIDs == nil {
			return store.NewPromotionInput{}, "mutually_exclusive_rule_ids is required and must be an array"
		}

		rules = append(rules, store.Rule{
			ID:                       ruleID,
			Kind:                     *requested.Kind,
			MinOrderAmountCents:      *requested.MinOrderAmountCents,
			DiscountAmountCents:      *requested.DiscountAmountCents,
			Stackable:                *requested.Stackable,
			MutuallyExclusiveRuleIDs: *requested.MutuallyExclusiveRuleIDs,
		})
	}

	for _, rule := range rules {
		for _, referencedID := range rule.MutuallyExclusiveRuleIDs {
			if referencedID == rule.ID {
				return store.NewPromotionInput{}, "mutually_exclusive_rule_ids can only reference other rules in the request"
			}
			if _, exists := seenIDs[referencedID]; !exists {
				return store.NewPromotionInput{}, "mutually_exclusive_rule_ids references an unknown rule id"
			}
		}
	}

	return store.NewPromotionInput{
		Name:             *request.Name,
		Rules:            rules,
		BudgetTotalCents: *request.BudgetTotalCents,
	}, ""
}

func getPromotion(st *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if strings.TrimSpace(id) == "" {
			writeAPIError(c, http.StatusNotFound, "promotion_not_found", "no promotion exists for the given id")
			return
		}
		promotion, err := st.GetPromotion(c.Request.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(c, http.StatusNotFound, "promotion_not_found", "no promotion exists for the given id")
			return
		}
		if err != nil {
			log.Printf("get promotion %q: %v", id, err)
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "the promotion could not be loaded")
			return
		}
		c.JSON(http.StatusOK, gin.H{"promotion": promotion})
	}
}
