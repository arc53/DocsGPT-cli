package pricing

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"docsgpt-cli/internal/bench/spec"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCostAndOverrides(t *testing.T) {
	tb := New(map[string]spec.ModelPricing{
		"m1":    {InputPerMillion: 1, OutputPerMillion: 10},
		"empty": {},
	})
	if tb.Len() != 1 || !tb.Has("m1") || tb.Has("empty") {
		t.Fatalf("overrides not seeded: len=%d", tb.Len())
	}
	cost, ok := tb.Cost("m1", 1_000_000, 100_000)
	if !ok || !near(cost, 2) {
		t.Errorf("Cost = %v, %v", cost, ok)
	}
	if _, ok := tb.Cost("unknown", 10, 10); ok {
		t.Errorf("unknown model must report no cost")
	}
	if _, ok := tb.Cost("", 10, 10); ok {
		t.Errorf("empty model must report no cost")
	}
	var nilTable *Table
	if _, ok := nilTable.Cost("m1", 1, 1); ok {
		t.Errorf("nil table must report no cost")
	}
}

func TestFetchShapes(t *testing.T) {
	body := `{
	  "default_model_id": "per-token",
	  "models": [
	    {"id": "per-token", "input_cost_per_token": 0.000001, "output_cost_per_token": 0.00001},
	    {"id": "per-million", "input_cost_per_million": 2.5, "output_cost_per_million": 7.5},
	    {"id": "nested", "pricing": {"input": 0.5, "output": 1.5}},
	    {"id": "nested-pm", "pricing": {"input_per_million": 3, "output_per_million": 4}},
	    {"id": "prompt-completion", "pricing": {"prompt": 1, "completion": 2}},
	    {"id": "overridden", "input_cost_per_million": 100, "output_cost_per_million": 100},
	    {"id": "no-price"}
	  ]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" {
			t.Errorf("path %q", r.URL.Path)
		}
		io.WriteString(w, body)
	}))
	defer srv.Close()

	tb := New(map[string]spec.ModelPricing{"overridden": {InputPerMillion: 1, OutputPerMillion: 1}})
	if err := tb.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if tb.DefaultModelID() != "per-token" {
		t.Errorf("DefaultModelID = %q", tb.DefaultModelID())
	}
	want := map[string][2]float64{
		"per-token":         {1, 10},
		"per-million":       {2.5, 7.5},
		"nested":            {0.5, 1.5},
		"nested-pm":         {3, 4},
		"prompt-completion": {1, 2},
		"overridden":        {1, 1}, // bench.yaml wins
	}
	for id, w := range want {
		cost, ok := tb.Cost(id, 1_000_000, 1_000_000)
		if !ok || !near(cost, w[0]+w[1]) {
			t.Errorf("%s: cost=%v ok=%v want %v", id, cost, ok, w[0]+w[1])
		}
	}
	if tb.Has("no-price") {
		t.Errorf("no-price should not be priced")
	}
}

func TestFetchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"success":false}`)
	}))
	defer srv.Close()
	tb := New(nil)
	if err := tb.Fetch(context.Background(), srv.URL); err == nil {
		t.Errorf("401 should be an error")
	}
	if err := tb.Fetch(context.Background(), "http://127.0.0.1:1"); err == nil {
		t.Errorf("connection refused should be an error")
	}
	if tb.Len() != 0 {
		t.Errorf("failed fetches must not add prices")
	}
}
