// Package pricing turns token usage into an estimated USD cost. Prices come
// from GET /api/models when the server exposes them, with bench.yaml
// `pricing:` entries as overrides/fallbacks. Anything unknown yields no cost
// (the report simply omits the column) rather than an error.
package pricing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"docsgpt-cli/internal/bench/spec"

	"github.com/tidwall/gjson"
)

// Table maps model ids to prices. It is safe for concurrent use.
type Table struct {
	mu             sync.RWMutex
	defaultModelID string
	prices         map[string]spec.ModelPricing
}

// DefaultModelID is the registry default reported by /api/models ("" if
// unknown).
func (t *Table) DefaultModelID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.defaultModelID
}

// New returns an empty table seeded with overrides (may be nil).
func New(overrides map[string]spec.ModelPricing) *Table {
	t := &Table{prices: map[string]spec.ModelPricing{}}
	t.Merge(overrides)
	return t
}

// Merge adds or replaces prices.
func (t *Table) Merge(m map[string]spec.ModelPricing) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, p := range m {
		if id != "" && (p.InputPerMillion > 0 || p.OutputPerMillion > 0) {
			t.prices[id] = p
		}
	}
}

// Has reports whether a price is known for model.
func (t *Table) Has(model string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.prices[model]
	return ok
}

// Len returns the number of priced models.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.prices)
}

// Cost estimates the USD cost of usage on model. ok is false when the model
// or its price is unknown, or usage is nil.
func (t *Table) Cost(model string, promptTokens, completionTokens int) (cost float64, ok bool) {
	if t == nil || model == "" {
		return 0, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, found := t.prices[model]
	if !found {
		return 0, false
	}
	cost = float64(promptTokens)*p.InputPerMillion/1e6 + float64(completionTokens)*p.OutputPerMillion/1e6
	return cost, true
}

// httpClient bounds the catalog fetch; it never blocks a run for long.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// Fetch loads GET {baseURL}/api/models into t (merging with any overrides
// already present; explicit overrides win). It tolerates every known shape of
// the pricing fields and returns an error only for transport/HTTP failures —
// callers treat that as "no server pricing" and continue.
func (t *Table) Fetch(ctx context.Context, baseURL string) error {
	url := strings.TrimRight(baseURL, "/") + "/api/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %d", url, resp.StatusCode)
	}
	if !gjson.ValidBytes(body) {
		return fmt.Errorf("GET %s returned non-JSON", url)
	}
	doc := gjson.ParseBytes(body)
	t.mu.Lock()
	defer t.mu.Unlock()
	if d := doc.Get("default_model_id").String(); d != "" {
		t.defaultModelID = d
	}
	models := doc.Get("models")
	if !models.IsArray() {
		models = doc.Get("data") // OpenAI-style list
	}
	models.ForEach(func(_, m gjson.Result) bool {
		id := m.Get("id").String()
		if id == "" {
			return true
		}
		if _, overridden := t.prices[id]; overridden {
			return true // bench.yaml wins
		}
		if p, ok := readPricing(m); ok {
			t.prices[id] = p
		}
		return true
	})
	return nil
}

// readPricing accepts, in order of preference:
//   - input_cost_per_token / output_cost_per_token (USD per token)
//   - input_cost_per_million / output_cost_per_million (USD per 1M tokens)
//   - pricing.{input,output}_per_million, pricing.{input,output} (per 1M)
//   - pricing.{prompt,completion} (per 1M)
func readPricing(m gjson.Result) (spec.ModelPricing, bool) {
	get := func(paths ...string) (float64, bool) {
		for _, p := range paths {
			if r := m.Get(p); r.Exists() && r.Type == gjson.Number {
				return r.Float(), true
			}
		}
		return 0, false
	}
	if in, ok := get("input_cost_per_token"); ok {
		out, _ := get("output_cost_per_token")
		return spec.ModelPricing{InputPerMillion: in * 1e6, OutputPerMillion: out * 1e6}, in > 0 || out > 0
	}
	if in, ok := get("input_cost_per_million", "pricing.input_per_million", "pricing.input", "pricing.prompt"); ok {
		out, _ := get("output_cost_per_million", "pricing.output_per_million", "pricing.output", "pricing.completion")
		return spec.ModelPricing{InputPerMillion: in, OutputPerMillion: out}, in > 0 || out > 0
	}
	return spec.ModelPricing{}, false
}
