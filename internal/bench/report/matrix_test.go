package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"docsgpt-cli/internal/bench/runner"
	"docsgpt-cli/internal/bench/target"
)

func fptr(f float64) *float64 { return &f }

// richCase is a passing case with TTFT, cost, and a judge verdict.
func richCase(name string, latencyMS, ttftMS int64, cost, judge float64, tokens int) *runner.CaseResult {
	return &runner.CaseResult{
		Name: name, Target: "stream", Status: runner.StatusPass, Repeat: 1, RequiredPass: 1, PassedRuns: 1, DurationMS: latencyMS,
		Runs: []*runner.RunResult{{
			Index: 1, Status: runner.StatusPass, Answer: "a", LatencyMS: latencyMS, FirstOutputMS: ttftMS,
			CostUSD: fptr(cost), Usage: &target.Usage{TotalTokens: tokens},
			Judge: &runner.JudgeResult{Score: judge, MinScore: 0.7, Reasoning: "fine"},
		}},
	}
}

func modelRun(model string, cases ...*runner.CaseResult) *runner.SuiteResult {
	r := &runner.SuiteResult{Suite: "s", AgentLabel: "bench-default", Model: model, Cases: cases}
	for _, c := range cases {
		switch c.Status {
		case runner.StatusPass:
			r.Totals.Pass++
		case runner.StatusFail:
			r.Totals.Fail++
		case runner.StatusSkip:
			r.Totals.Skip++
		case runner.StatusError:
			r.Totals.Error++
		}
	}
	return r
}

func TestMatrixSummaryAndTable(t *testing.T) {
	a := modelRun("model-a", richCase("c1", 1000, 100, 0.01, 0.9, 500), richCase("c2", 3000, 300, 0.03, 0.7, 1500), skipCase("c3", "n/a"))
	b := modelRun("model-b", richCase("c1", 2000, 900, 0.002, 0.5, 400), failCase("c2"), skipCase("c3", "n/a"))
	m := NewMatrix([]string{"model-a", "model-b"}, []*runner.SuiteResult{a, b})

	if m.Kind != "matrix" || len(m.Summary) != 2 || len(m.Models) != 2 {
		t.Fatalf("matrix = %+v", m)
	}
	sa := m.Summary[0]
	if sa.Model != "model-a" || sa.Pass != 2 || sa.Cases != 3 || sa.Skip != 1 || sa.PassRate != 1 {
		t.Errorf("summary a = %+v", sa)
	}
	if sa.JudgeMean == nil || *sa.JudgeMean < 0.79 || *sa.JudgeMean > 0.81 || sa.JudgeN != 2 {
		t.Errorf("judge mean a = %v n=%d", sa.JudgeMean, sa.JudgeN)
	}
	if sa.P50MS != 1000 || sa.P95MS != 3000 {
		t.Errorf("p50/p95 a = %v/%v", sa.P50MS, sa.P95MS)
	}
	if sa.TTFTP50MS == nil || *sa.TTFTP50MS != 100 || sa.TTFTP95MS == nil || *sa.TTFTP95MS != 300 {
		t.Errorf("ttft a = %v/%v", sa.TTFTP50MS, sa.TTFTP95MS)
	}
	if sa.TokensPerCase == nil || *sa.TokensPerCase != 1000 {
		t.Errorf("tokens/case a = %v", sa.TokensPerCase)
	}
	if sa.CostPerCase == nil || *sa.CostPerCase < 0.0199 || *sa.CostPerCase > 0.0201 || sa.TotalCost == nil || *sa.TotalCost < 0.0399 {
		t.Errorf("cost a = %v total %v", sa.CostPerCase, sa.TotalCost)
	}
	sb := m.Summary[1]
	if sb.Pass != 1 || sb.Fail != 1 || sb.PassRate != 0.5 {
		t.Errorf("summary b = %+v", sb)
	}
	if m.Results["model-b"]["c2"] == nil || m.Results["model-b"]["c2"].Status != runner.StatusFail {
		t.Errorf("results[model][case] lookup broken: %+v", m.Results)
	}

	var buf bytes.Buffer
	Matrix(&buf, m)
	out := buf.String()
	for _, want := range []string{"Model matrix", "model-a", "model-b", "Judge", "TTFT p50", "Tok/case", "$/case", "2/2", "100%", "1/2", "50%", "0.80", "0.10s"} {
		if !strings.Contains(out, want) {
			t.Errorf("matrix table missing %q\n---\n%s", want, out)
		}
	}

	buf.Reset()
	if err := MatrixJSON(&buf, m); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("matrix json: %v", err)
	}
	results := doc["results"].(map[string]any)
	if _, ok := results["model-a"].(map[string]any)["c1"]; !ok {
		t.Errorf("json results not keyed [model][case]: %v", results)
	}
	if doc["kind"] != "matrix" || doc["schema_version"].(float64) != float64(runner.SchemaVersion) {
		t.Errorf("json header: %v %v", doc["kind"], doc["schema_version"])
	}
}

func TestMatrixOmitsUnavailableColumns(t *testing.T) {
	// No judge, no TTFT, no cost, no tokens → only the base columns.
	a := modelRun("m1", passCase("c1", 0))
	a.Cases[0].Runs[0].Usage = nil
	m := NewMatrix([]string{"m1"}, []*runner.SuiteResult{a})
	var buf bytes.Buffer
	Matrix(&buf, m)
	out := buf.String()
	for _, absent := range []string{"Judge", "TTFT", "Tok/case", "$/case"} {
		if strings.Contains(out, absent) {
			t.Errorf("column %q should be omitted\n%s", absent, out)
		}
	}
	if !strings.Contains(out, "p50") || !strings.Contains(out, "m1") {
		t.Errorf("base columns missing\n%s", out)
	}
}

func TestCompareLabelsByModel(t *testing.T) {
	a := modelRun("model-a", passCase("c1", 1))
	b := modelRun("model-b", passCase("c1", 1))
	var buf bytes.Buffer
	Compare(&buf, []*runner.SuiteResult{a, b})
	if out := buf.String(); !strings.Contains(out, "model-a") || !strings.Contains(out, "model-b") {
		t.Errorf("same agent, different models → columns should be labelled by model\n%s", out)
	}
	// Different agents, same model → agent labels.
	b.AgentLabel, b.Model = "other-agent", "model-a"
	buf.Reset()
	Compare(&buf, []*runner.SuiteResult{a, b})
	if out := buf.String(); !strings.Contains(out, "bench-default") || !strings.Contains(out, "other-agent") {
		t.Errorf("different agents → columns should be labelled by agent\n%s", out)
	}
}

func TestPrettyShowsModelTTFTCostAndStats(t *testing.T) {
	r := modelRun("model-a", richCase("c1", 1000, 100, 0.0123, 0.9, 500), richCase("c2", 3000, 300, 0.03, 0.7, 1500))
	r.RunTag = "nightly"
	var buf bytes.Buffer
	Pretty(&buf, r, false)
	out := buf.String()
	for _, want := range []string{"model: model-a", "tag: nightly", "ttft 0.10s", "$0.012", "latency p50", "ttft p50", "judge mean 0.80", "$0.042"} {
		if !strings.Contains(out, want) {
			t.Errorf("pretty missing %q\n---\n%s", want, out)
		}
	}
}

func TestPrettyVerboseTurnsAndErrorResponse(t *testing.T) {
	c := &runner.CaseResult{Name: "mt", Target: "stream", Status: runner.StatusPass, Repeat: 1, RequiredPass: 1, PassedRuns: 1,
		Runs: []*runner.RunResult{{Index: 1, Status: runner.StatusPass, Answer: "final",
			Turns: []*runner.TurnResult{{Index: 1, Question: "q1", Answer: "a1"}, {Index: 2, Question: "q2", Answer: "final"}}}}}
	neg := &runner.CaseResult{Name: "neg", Target: "v1", Status: runner.StatusPass, Repeat: 1, RequiredPass: 1, PassedRuns: 1,
		Runs: []*runner.RunResult{{Index: 1, Status: runner.StatusPass, ErrorResponse: &runner.ErrorResponse{Status: 401, Message: "Invalid API key"}}}}
	r := modelRun("", c, neg)
	var buf bytes.Buffer
	Pretty(&buf, r, true)
	out := buf.String()
	for _, want := range []string{"run 1 turn 1: q1", "a1", "run 1 turn 2: q2", "server error (HTTP 401)", "Invalid API key"} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "run 1 answer:") {
		t.Errorf("multi-turn cases should not also print the flat answer\n%s", out)
	}
}

func TestPercentile(t *testing.T) {
	vals := []float64{5, 1, 4, 2, 3}
	if percentile(vals, 50) != 3 || percentile(vals, 0) != 1 || percentile(vals, 100) != 5 || percentile(nil, 50) != 0 {
		t.Errorf("percentile: %v %v %v", percentile(vals, 50), percentile(vals, 0), percentile(vals, 100))
	}
	if p := percentile([]float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}, 95); p != 100 {
		t.Errorf("p95 of 10 = %v", p)
	}
	if mean([]float64{1, 2, 3}) != 2 || mean(nil) != 0 {
		t.Errorf("mean")
	}
}
