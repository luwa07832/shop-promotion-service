package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/luwa07832/shop-promotion-service/internal/store"
)

func validPromotionBody() string {
	return `{
		"name": "国庆满减",
		"budget_total_cents": 100000,
		"rules": [
			{
				"id": "r1",
				"kind": "order_threshold",
				"min_order_amount_cents": 5000,
				"discount_amount_cents": 500,
				"stackable": true,
				"mutually_exclusive_rule_ids": ["r2"]
			},
			{
				"id": "r2",
				"kind": "order_threshold",
				"min_order_amount_cents": 20000,
				"discount_amount_cents": 2000,
				"stackable": false,
				"mutually_exclusive_rule_ids": []
			}
		]
	}`
}

func serve(t *testing.T, st *store.Store, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, target, reader)
	NewRouter(st).ServeHTTP(recorder, request)
	return recorder
}

func postPromotion(t *testing.T, st *store.Store) map[string]any {
	t.Helper()
	recorder := serve(t, st, http.MethodPost, "/v1/promotions", validPromotionBody())
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	var response struct {
		Promotion map[string]any `json:"promotion"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v: %s", err, recorder.Body.String())
	}
	return response.Promotion
}

func TestCreatePromotionSuccess(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	promotion := postPromotion(t, st)

	if promotion["id"] == "" || promotion["id"] == nil {
		t.Fatalf("missing generated id: %v", promotion)
	}
	if promotion["name"] != "国庆满减" {
		t.Fatalf("name = %v", promotion["name"])
	}
	if promotion["budget_total_cents"] != float64(100000) {
		t.Fatalf("budget_total_cents = %v", promotion["budget_total_cents"])
	}
	if promotion["budget_used_cents"] != float64(0) {
		t.Fatalf("budget_used_cents = %v, want 0", promotion["budget_used_cents"])
	}
	if promotion["status"] != "draft" {
		t.Fatalf("status = %v, want draft", promotion["status"])
	}
	rules, ok := promotion["rules"].([]any)
	if !ok || len(rules) != 2 {
		t.Fatalf("rules = %v", promotion["rules"])
	}
	first := rules[0].(map[string]any)
	if first["id"] != "r1" || first["kind"] != "order_threshold" || first["stackable"] != true {
		t.Fatalf("unexpected first rule: %v", first)
	}
	exclusive, ok := first["mutually_exclusive_rule_ids"].([]any)
	if !ok || len(exclusive) != 1 || exclusive[0] != "r2" {
		t.Fatalf("unexpected exclusive ids: %v", first["mutually_exclusive_rule_ids"])
	}

	recorder := serve(t, st, http.MethodGet, "/v1/promotions/"+promotion["id"].(string), "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var fetched struct {
		Promotion map[string]any `json:"promotion"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fetched.Promotion["budget_used_cents"] != float64(0) || fetched.Promotion["status"] != "draft" {
		t.Fatalf("fetched promotion differs: %v", fetched.Promotion)
	}
	fetchedRules := fetched.Promotion["rules"].([]any)
	fetchedExclusive := fetchedRules[0].(map[string]any)["mutually_exclusive_rule_ids"].([]any)
	if len(fetchedExclusive) != 1 || fetchedExclusive[0] != "r2" {
		t.Fatalf("fetched exclusive ids = %v", fetchedExclusive)
	}
}

func TestCreatePromotionPersistsAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "service.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	promotion := postPromotion(t, st)
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	recorder := serve(t, reopened, http.MethodGet, "/v1/promotions/"+promotion["id"].(string), "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("get after restart status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestCreatePromotionRejectsInvalidInput(t *testing.T) {
	invalidBodies := map[string]string{
		"malformed json":            `{not json`,
		"empty name":                `{"name":"  ","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"missing name":              `{"budget_total_cents":100,"rules":[]}`,
		"missing rules":             `{"name":"n","budget_total_cents":100}`,
		"empty rules":               `{"name":"n","budget_total_cents":100,"rules":[]}`,
		"duplicate rule id":         `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]},{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"wrong kind":                `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"percent_off","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"missing kind":              `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"unknown mutex reference":   `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":["r9"]}]}`,
		"self mutex reference":      `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":["r1"]}]}`,
		"negative amount":           `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":-1,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"decimal amount":            `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":10.5,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"string amount":             `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":"100","discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"discount over threshold":   `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":10,"discount_amount_cents":11,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"zero budget":               `{"name":"n","budget_total_cents":0,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"negative budget":           `{"name":"n","budget_total_cents":-5,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]}`,
		"missing stackable":         `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"mutually_exclusive_rule_ids":[]}]}`,
		"missing mutex array":       `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true}]}`,
		"mutex array wrong type":    `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":"r2"}]}`,
		"empty rule id":             `{"name":"n","budget_total_cents":100,"rules":[{"id":"","kind":"order_threshold","min_order_amount_cents":100,"discount_amount_cents":10,"stackable":true,"mutually_exclusive_rule_ids":[]}]}`,
		"not an object":             `["n"]`,
		"multiple json values":      `{"name":"n","budget_total_cents":100,"rules":[]} {"name":"m"}`,
		"zero amounts accepted? no": `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":0,"discount_amount_cents":0,"stackable":false,"mutually_exclusive_rule_ids":[]}],"extra":true}`,
	}

	for name, body := range invalidBodies {
		t.Run(name, func(t *testing.T) {
			// The last case is actually valid input (unknown fields ignored); handle separately below.
			if name == "zero amounts accepted? no" {
				return
			}
			st, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer st.Close()

			recorder := serve(t, st, http.MethodPost, "/v1/promotions", body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode: %v: %s", err, recorder.Body.String())
			}
			if response.Error.Code != "invalid_promotion_input" {
				t.Fatalf("code = %q", response.Error.Code)
			}
			if response.Error.Message == "" {
				t.Fatalf("empty message")
			}
		})
	}
}

func TestCreatePromotionAcceptsZeroAmountsAndUnknownFields(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	body := `{"name":"n","budget_total_cents":100,"rules":[{"id":"r1","kind":"order_threshold","min_order_amount_cents":0,"discount_amount_cents":0,"stackable":false,"mutually_exclusive_rule_ids":[]}],"future_field":"ignored"}`
	recorder := serve(t, st, http.MethodPost, "/v1/promotions", body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestGetPromotionNotFound(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	for _, target := range []string{"/v1/promotions/does-not-exist", "/v1/promotions/"} {
		recorder := serve(t, st, http.MethodGet, target, "")
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404: %s", target, recorder.Code, recorder.Body.String())
		}
		var response struct {
			Error map[string]any `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if response.Error["code"] != "promotion_not_found" {
			t.Fatalf("%s code = %v", target, response.Error["code"])
		}
		if _, ok := response.Error["code"].(string); !ok {
			t.Fatalf("code not a string")
		}
		if _, ok := response.Error["message"].(string); !ok {
			t.Fatalf("message not a string")
		}
		if len(response.Error) != 2 {
			t.Fatalf("error object has extra fields: %v", response.Error)
		}
	}
}
