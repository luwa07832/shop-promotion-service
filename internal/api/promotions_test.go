package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luwa07832/shop-promotion-service/internal/store"
)

const validPromotionBody = `{
  "name": "双十一满减",
  "rules": [
    {
      "id": "r1",
      "kind": "order_threshold",
      "min_order_amount_cents": 10000,
      "discount_amount_cents": 1500,
      "stackable": false,
      "mutually_exclusive_rule_ids": ["r2"]
    },
    {
      "id": "r2",
      "kind": "order_threshold",
      "min_order_amount_cents": 20000,
      "discount_amount_cents": 3000,
      "stackable": true,
      "mutually_exclusive_rule_ids": []
    }
  ],
  "budget_total_cents": 500000
}`

type promotionResponse struct {
	Promotion struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		BudgetTotalCents int64  `json:"budget_total_cents"`
		BudgetUsedCents  int64  `json:"budget_used_cents"`
		Status           string `json:"status"`
		Rules            []struct {
			ID                       string   `json:"id"`
			Kind                     string   `json:"kind"`
			MinOrderAmountCents      int64    `json:"min_order_amount_cents"`
			DiscountAmountCents      int64    `json:"discount_amount_cents"`
			Stackable                bool     `json:"stackable"`
			MutuallyExclusiveRuleIDs []string `json:"mutually_exclusive_rule_ids"`
		} `json:"rules"`
	} `json:"promotion"`
}

func newTestRouter(t *testing.T, dbPath string) (*store.Store, http.Handler) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, NewRouter(st)
}

func postPromotion(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/promotions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	return recorder
}

func getPromotion(t *testing.T, handler http.Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/promotions/"+id, nil)
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodePromotionResponse(t *testing.T, recorder *httptest.ResponseRecorder) promotionResponse {
	t.Helper()
	var response promotionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return response
}

func TestCreatePromotionReturnsDraft(t *testing.T) {
	_, handler := newTestRouter(t, filepath.Join(t.TempDir(), "service.db"))

	recorder := postPromotion(t, handler, validPromotionBody)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}

	response := decodePromotionResponse(t, recorder)
	promotion := response.Promotion
	if promotion.ID == "" {
		t.Fatal("promotion id must be server-generated")
	}
	if promotion.Name != "双十一满减" {
		t.Fatalf("name = %q", promotion.Name)
	}
	if promotion.BudgetTotalCents != 500000 || promotion.BudgetUsedCents != 0 {
		t.Fatalf("budget = total %d used %d", promotion.BudgetTotalCents, promotion.BudgetUsedCents)
	}
	if promotion.Status != "draft" {
		t.Fatalf("status = %q, want draft", promotion.Status)
	}
	if len(promotion.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(promotion.Rules))
	}
	first := promotion.Rules[0]
	if first.ID != "r1" || first.Kind != "order_threshold" || first.MinOrderAmountCents != 10000 ||
		first.DiscountAmountCents != 1500 || first.Stackable {
		t.Fatalf("first rule mismatch: %+v", first)
	}
	if len(first.MutuallyExclusiveRuleIDs) != 1 || first.MutuallyExclusiveRuleIDs[0] != "r2" {
		t.Fatalf("exclusive ids = %v", first.MutuallyExclusiveRuleIDs)
	}
	if got := promotion.Rules[1].MutuallyExclusiveRuleIDs; len(got) != 0 {
		t.Fatalf("empty exclusive ids must encode as array, got %v", got)
	}
}

func TestGetPromotionReturnsSameObject(t *testing.T) {
	_, handler := newTestRouter(t, filepath.Join(t.TempDir(), "service.db"))

	created := decodePromotionResponse(t, postPromotion(t, handler, validPromotionBody))
	fetched := getPromotion(t, handler, created.Promotion.ID)
	if fetched.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", fetched.Code, http.StatusOK, fetched.Body.String())
	}

	read := decodePromotionResponse(t, fetched)
	if !reflect.DeepEqual(read.Promotion, created.Promotion) {
		t.Fatalf("read object %+v differs from created %+v", read.Promotion, created.Promotion)
	}

	again := getPromotion(t, handler, created.Promotion.ID)
	if again.Body.String() != fetched.Body.String() {
		t.Fatal("repeated reads must not change the promotion")
	}
}

func TestPromotionSurvivesStoreReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "service.db")
	_, handler := newTestRouter(t, dbPath)
	created := decodePromotionResponse(t, postPromotion(t, handler, validPromotionBody))

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	reopened := NewRouter(st)

	fetched := getPromotion(t, reopened, created.Promotion.ID)
	if fetched.Code != http.StatusOK {
		t.Fatalf("status after reopen = %d: %s", fetched.Code, fetched.Body.String())
	}
	if !reflect.DeepEqual(decodePromotionResponse(t, fetched).Promotion, created.Promotion) {
		t.Fatal("promotion must be readable after restart")
	}
}

func TestInvalidPromotionInput(t *testing.T) {
	bodies := map[string]string{
		"invalid json":              `{not json`,
		"trailing value":            validPromotionBody + " 42",
		"unknown field":             `{"name":"x","rules":[],"budget_total_cents":1,"extra":1}`,
		"name missing":              `{"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`,
		"name empty":                `{"name":"   ","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`,
		"rules missing":             `{"name":"x","budget_total_cents":1}`,
		"rules empty":               `{"name":"x","rules":[],"budget_total_cents":1}`,
		"rule id missing":           `{"name":"x","rules":[{"kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`,
		"rule id duplicate":         `{"name":"x","rules":[{"id":"r","kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":[]},{"id":"r","kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`,
		"wrong kind":                `{"name":"x","rules":[{"id":"r1","kind":"percent","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`,
		"missing exclusive ids":     `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true}],"budget_total_cents":1}`,
		"unknown exclusive ref":     `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":["r2"]}],"budget_total_cents":1}`,
		"self exclusive ref":        `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":["r1"]}],"budget_total_cents":1}`,
		"negative min amount":       `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":-1,"discount_amount_cents":0,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`,
		"fractional amount":         `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":1.5,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`,
		"amount as string":          `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":"100","discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`,
		"discount over threshold":   `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":101,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`,
		"stackable missing":         `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`,
		"budget missing":            `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"budget zero":               `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":1,"discount_amount_cents":1,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":0}`,
		"zero amounts are allowed?": `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":0,"discount_amount_cents":0,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":-1}`,
	}

	// The last body is invalid only because of the negative budget; verify it explicitly
	// under a stable case name instead of the question above.
	bodies["negative budget"] = bodies["zero amounts are allowed?"]
	delete(bodies, "zero amounts are allowed?")

	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			_, handler := newTestRouter(t, filepath.Join(t.TempDir(), "service.db"))
			recorder := postPromotion(t, handler, body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			assertErrorCode(t, recorder, "invalid_promotion_input")
		})
	}
}

func TestZeroAmountsAndBudgetAreAccepted(t *testing.T) {
	_, handler := newTestRouter(t, filepath.Join(t.TempDir(), "service.db"))
	body := `{"name":"x","rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":0,"discount_amount_cents":0,"stackable":true,"mutually_exclusive_rule_ids":[]}],"budget_total_cents":1}`
	recorder := postPromotion(t, handler, body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPromotionNotFound(t *testing.T) {
	_, handler := newTestRouter(t, filepath.Join(t.TempDir(), "service.db"))

	for _, id := range []string{
		"00000000-0000-0000-0000-000000000000",
		"not-a-uuid",
		"%20",
		"123",
	} {
		recorder := getPromotion(t, handler, id)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("id %q: status = %d, want %d", id, recorder.Code, http.StatusNotFound)
		}
		assertErrorCode(t, recorder, "promotion_not_found")
	}
}

func TestUnknownPromotionRouteStillUsesRouteNotFound(t *testing.T) {
	_, handler := newTestRouter(t, filepath.Join(t.TempDir(), "service.db"))
	for _, path := range []string{"/v1/promotions", "/v1/other", "/v1/promotions/"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("path %q: status = %d", path, recorder.Code)
		}
		assertErrorCode(t, recorder, "route_not_found")
	}
}

func assertErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	if len(payload) != 1 {
		t.Fatalf("response has top-level keys beyond error: %v", payload)
	}
	errorValue, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error is not an object: %v", payload)
	}
	if len(errorValue) != 2 {
		t.Fatalf("error must contain only code and message: %v", errorValue)
	}
	code, _ := errorValue["code"].(string)
	message, _ := errorValue["message"].(string)
	if code != wantCode || message == "" {
		t.Fatalf("error = %v, want code %q with message", errorValue, wantCode)
	}
	lowered := strings.ToLower(message)
	for _, leak := range []string{"sql", "select", "insert", ".go", "goroutine", "/"} {
		if strings.Contains(lowered, leak) {
			t.Fatalf("error message leaks %q: %s", leak, message)
		}
	}
}
