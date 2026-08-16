package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"docsgpt-cli/internal/bench/runner"
	"docsgpt-cli/internal/display"
)

// MatrixSummary is one model's row of the comparison table.
type MatrixSummary struct {
	Model         string   `json:"model"`
	Cases         int      `json:"cases"`
	Pass          int      `json:"pass"`
	Fail          int      `json:"fail"`
	Error         int      `json:"error"`
	Skip          int      `json:"skip"`
	PassRate      float64  `json:"pass_rate"` // pass / (cases - skip), 0..1
	JudgeMean     *float64 `json:"judge_mean,omitempty"`
	JudgeN        int      `json:"judge_n,omitempty"`
	P50MS         float64  `json:"p50_ms"`
	P95MS         float64  `json:"p95_ms"`
	TTFTP50MS     *float64 `json:"ttft_p50_ms,omitempty"`
	TTFTP95MS     *float64 `json:"ttft_p95_ms,omitempty"`
	TokensPerCase *float64 `json:"tokens_per_case,omitempty"`
	CostPerCase   *float64 `json:"cost_per_case_usd,omitempty"`
	TotalCost     *float64 `json:"total_cost_usd,omitempty"`
	DurationMS    int64    `json:"duration_ms"`
}

// MatrixResult is the JSON document of a `bench --matrix` run: one full
// SuiteResult per model plus the derived comparison rows. Results are keyed
// results[model][case] for direct lookup.
type MatrixResult struct {
	SchemaVersion int                                      `json:"schema_version"`
	Kind          string                                   `json:"kind"` // "matrix"
	Suite         string                                   `json:"suite"`
	Dir           string                                   `json:"dir"`
	StartedAt     time.Time                                `json:"started_at"`
	DurationMS    int64                                    `json:"duration_ms"`
	Models        []string                                 `json:"models"`
	Summary       []MatrixSummary                          `json:"summary"`
	Results       map[string]map[string]*runner.CaseResult `json:"results"`
	Runs          []*runner.SuiteResult                    `json:"runs"`
}

// NewMatrix folds per-model suite results (in matrix order) into a
// MatrixResult.
func NewMatrix(models []string, runs []*runner.SuiteResult) *MatrixResult {
	m := &MatrixResult{
		SchemaVersion: runner.SchemaVersion,
		Kind:          "matrix",
		Models:        models,
		Results:       make(map[string]map[string]*runner.CaseResult, len(runs)),
		Runs:          runs,
	}
	for i, r := range runs {
		if r == nil {
			continue
		}
		if m.Suite == "" {
			m.Suite, m.Dir, m.StartedAt = r.Suite, r.Dir, r.StartedAt
		}
		m.DurationMS += r.DurationMS
		model := r.Model
		if i < len(models) {
			model = models[i]
		}
		m.Summary = append(m.Summary, summarize(model, r))
		byCase := make(map[string]*runner.CaseResult, len(r.Cases))
		for _, c := range r.Cases {
			byCase[c.Name] = c
		}
		m.Results[model] = byCase
	}
	return m
}

// summarize computes one model's row.
func summarize(model string, r *runner.SuiteResult) MatrixSummary {
	s := MatrixSummary{
		Model:      model,
		Cases:      len(r.Cases),
		Pass:       r.Totals.Pass,
		Fail:       r.Totals.Fail,
		Error:      r.Totals.Error,
		Skip:       r.Totals.Skip,
		DurationMS: r.DurationMS,
	}
	if scored := s.Cases - s.Skip; scored > 0 {
		s.PassRate = float64(s.Pass) / float64(scored)
	}
	if js := judgeScores(r); len(js) > 0 {
		m := mean(js)
		s.JudgeMean, s.JudgeN = &m, len(js)
	}
	lat := runLatencies(r)
	s.P50MS, s.P95MS = percentile(lat, 50), percentile(lat, 95)
	if ttft := runFirstOutputs(r); len(ttft) > 0 {
		p50, p95 := percentile(ttft, 50), percentile(ttft, 95)
		s.TTFTP50MS, s.TTFTP95MS = &p50, &p95
	}
	executed := 0
	for _, c := range r.Cases {
		if c.Status != runner.StatusSkip {
			executed++
		}
	}
	if tok := suiteTokens(r); tok > 0 && executed > 0 {
		v := float64(tok) / float64(executed)
		s.TokensPerCase = &v
	}
	if cost, ok := suiteCost(r); ok && executed > 0 {
		per := cost / float64(executed)
		s.CostPerCase, s.TotalCost = &per, &cost
	}
	return s
}

// MatrixJSON writes the matrix document.
func MatrixJSON(w io.Writer, m *MatrixResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// Matrix writes the per-model comparison table: pass rate, mean judge score,
// p50/p95 wall, TTFT, tokens/case, cost/case. Columns whose data no model
// reported are omitted.
func Matrix(w io.Writer, m *MatrixResult) {
	if m == nil || len(m.Summary) == 0 {
		return
	}
	hasJudge, hasTTFT, hasTokens, hasCost := false, false, false, false
	for _, s := range m.Summary {
		hasJudge = hasJudge || s.JudgeMean != nil
		hasTTFT = hasTTFT || s.TTFTP50MS != nil
		hasTokens = hasTokens || s.TokensPerCase != nil
		hasCost = hasCost || s.CostPerCase != nil
	}

	headers := []string{"Model", "Pass", "Rate"}
	if hasJudge {
		headers = append(headers, "Judge")
	}
	headers = append(headers, "p50", "p95")
	if hasTTFT {
		headers = append(headers, "TTFT p50", "TTFT p95")
	}
	if hasTokens {
		headers = append(headers, "Tok/case")
	}
	if hasCost {
		headers = append(headers, "$/case")
	}

	rows := make([][]string, 0, len(m.Summary))
	for _, s := range m.Summary {
		row := []string{
			s.Model,
			fmt.Sprintf("%d/%d", s.Pass, s.Cases-s.Skip),
			fmt.Sprintf("%.0f%%", 100*s.PassRate),
		}
		if hasJudge {
			row = append(row, optFloat(s.JudgeMean, "%.2f"))
		}
		row = append(row, fmt.Sprintf("%.2fs", s.P50MS/1000), fmt.Sprintf("%.2fs", s.P95MS/1000))
		if hasTTFT {
			row = append(row, optSeconds(s.TTFTP50MS), optSeconds(s.TTFTP95MS))
		}
		if hasTokens {
			row = append(row, optFloat(s.TokensPerCase, "%.0f"))
		}
		if hasCost {
			if s.CostPerCase != nil {
				row = append(row, formatUSD(*s.CostPerCase))
			} else {
				row = append(row, "-")
			}
		}
		rows = append(rows, row)
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	fmt.Fprintln(w, display.Accent("Model matrix")+" "+display.Muted(fmt.Sprintf("(%d models × %d cases)", len(m.Summary), casesInMatrix(m))))
	var hb strings.Builder
	for i, h := range headers {
		if i > 0 {
			hb.WriteString("  ")
		}
		hb.WriteString(pad(h, widths[i]))
	}
	fmt.Fprintln(w, display.Muted(hb.String()))
	for ri, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(pad(cell, widths[i]))
		}
		line := b.String()
		switch {
		case m.Summary[ri].Fail+m.Summary[ri].Error == 0:
			line = display.Success(line)
		case m.Summary[ri].Pass == 0:
			line = display.Danger(line)
		}
		fmt.Fprintln(w, line)
	}
}

func casesInMatrix(m *MatrixResult) int {
	n := 0
	for _, s := range m.Summary {
		if s.Cases > n {
			n = s.Cases
		}
	}
	return n
}

func optFloat(v *float64, format string) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf(format, *v)
}

func optSeconds(ms *float64) string {
	if ms == nil {
		return "-"
	}
	return fmt.Sprintf("%.2fs", *ms/1000)
}
