package report

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"docsgpt-cli/internal/bench/assert"
	"docsgpt-cli/internal/bench/runner"
	"docsgpt-cli/internal/bench/target"
	"docsgpt-cli/internal/display"
)

// xmlValid reports whether s is well-formed XML.
func xmlValid(s string) error {
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// TestMain forces the unstyled theme so report output is deterministic (no ANSI).
func TestMain(m *testing.M) {
	display.UsePlainTheme()
	os.Exit(m.Run())
}

// --- fixtures --------------------------------------------------------------

func passCase(name string, tokens int) *runner.CaseResult {
	return &runner.CaseResult{
		Name: name, Target: "v1", Status: runner.StatusPass, Repeat: 1, RequiredPass: 1,
		PassedRuns: 1, DurationMS: 1230,
		Runs: []*runner.RunResult{{
			Index: 1, Status: runner.StatusPass, Answer: "an answer",
			Usage: &target.Usage{TotalTokens: tokens}, LatencyMS: 1230,
		}},
	}
}

func failCase(name string) *runner.CaseResult {
	return &runner.CaseResult{
		Name: name, Target: "v1", Status: runner.StatusFail, Repeat: 1, RequiredPass: 1,
		DurationMS: 800,
		Runs: []*runner.RunResult{{
			Index: 1, Status: runner.StatusFail, Answer: "wrong",
			Assertions: []assert.Result{
				{Name: `answer contains "foo"`, Status: assert.StatusFail, Message: `answer does not contain "foo"`},
				{Name: "json status", Status: assert.StatusPass},
			},
		}},
	}
}

func errorCase(name, msg string) *runner.CaseResult {
	return &runner.CaseResult{
		Name: name, Target: "v1", Status: runner.StatusError, Repeat: 1, RequiredPass: 1,
		DurationMS: 100,
		Runs:       []*runner.RunResult{{Index: 1, Status: runner.StatusError, Error: msg}},
	}
}

func skipCase(name, reason string) *runner.CaseResult {
	return &runner.CaseResult{Name: name, Target: "v1", Status: runner.StatusSkip, SkipReason: reason}
}

func sampleSuite() *runner.SuiteResult {
	return &runner.SuiteResult{
		SchemaVersion: 1, Suite: "demo", Dir: "/tmp/demo", AgentLabel: "my-agent", Target: "v1",
		StartedAt: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), DurationMS: 5000,
		Cases: []*runner.CaseResult{
			passCase("alpha", 18),
			failCase("beta"),
			skipCase("gamma", "not ready"),
			errorCase("delta", "v1 target: 500 boom"),
		},
		Totals: runner.Totals{Pass: 1, Fail: 1, Skip: 1, Error: 1},
	}
}

// --- tests -----------------------------------------------------------------

func TestJSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, sampleSuite()); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got runner.SuiteResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if got.SchemaVersion != 1 || got.Suite != "demo" || len(got.Cases) != 4 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Totals != (runner.Totals{Pass: 1, Fail: 1, Skip: 1, Error: 1}) {
		t.Errorf("totals = %+v", got.Totals)
	}
}

func TestPretty(t *testing.T) {
	var buf bytes.Buffer
	Pretty(&buf, sampleSuite(), false)
	out := buf.String()

	for _, want := range []string{
		"docsgpt bench", "demo", "agent: my-agent", "target: v1",
		"PASS ", "alpha", "18 tok",
		"FAIL ", "beta", `answer contains "foo"`, "answer does not contain",
		"SKIP ", "gamma", "(not ready)",
		"ERROR", "delta", "v1 target: 500 boom",
		"1 passed", "1 failed", "1 skipped", "1 errored",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Pretty output missing %q\n---\n%s", want, out)
		}
	}
	// A passing assertion should not be listed under a failed case.
	if strings.Contains(out, "json status") {
		t.Errorf("passing assertion should not appear in failure detail:\n%s", out)
	}
}

func TestPrettyVerbose(t *testing.T) {
	sr := &runner.SuiteResult{
		Suite: "d", Cases: []*runner.CaseResult{{
			Name: "c", Target: "v1", Status: runner.StatusPass, Repeat: 1, RequiredPass: 1, PassedRuns: 1,
			Runs: []*runner.RunResult{{
				Index: 1, Status: runner.StatusPass, Answer: "the long form answer text",
				Assertions: []assert.Result{{Name: "judge", Status: assert.StatusPass, Message: "score 0.90 (min 0.70): well argued"}},
			}},
		}},
		Totals: runner.Totals{Pass: 1},
	}
	var buf bytes.Buffer
	Pretty(&buf, sr, true)
	out := buf.String()
	if !strings.Contains(out, "the long form answer text") {
		t.Errorf("verbose should echo the answer:\n%s", out)
	}
	if !strings.Contains(out, "well argued") {
		t.Errorf("verbose should echo judge reasoning:\n%s", out)
	}
}

func TestJUnit(t *testing.T) {
	var buf bytes.Buffer
	if err := JUnit(&buf, sampleSuite()); err != nil {
		t.Fatalf("JUnit: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<?xml version="1.0"`,
		`<testsuite name="demo" tests="4" failures="1" errors="1" skipped="1"`,
		`<testcase classname="demo" name="alpha"`,
		`<failure message="assertions failed">`,
		`answer does not contain`,
		`<error message="run errored">`,
		`v1 target: 500 boom`,
		`<skipped message="not ready">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JUnit output missing %q\n---\n%s", want, out)
		}
	}
	// Verify it is valid XML.
	if err := xmlValid(out); err != nil {
		t.Errorf("JUnit output is not valid XML: %v", err)
	}
}

func TestJUnitEscaping(t *testing.T) {
	sr := &runner.SuiteResult{
		Suite: "s", Totals: runner.Totals{Error: 1},
		Cases: []*runner.CaseResult{errorCase("c", `boom <tag> & "quote"`)},
	}
	var buf bytes.Buffer
	if err := JUnit(&buf, sr); err != nil {
		t.Fatalf("JUnit: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "<tag>") {
		t.Errorf("angle brackets not escaped:\n%s", out)
	}
	if !strings.Contains(out, "&lt;tag&gt;") || !strings.Contains(out, "&amp;") {
		t.Errorf("expected escaped entities:\n%s", out)
	}
}

func TestCompare(t *testing.T) {
	a := &runner.SuiteResult{
		Suite: "s", AgentLabel: "agent-A", Totals: runner.Totals{Pass: 1, Fail: 1},
		Cases: []*runner.CaseResult{passCase("shared", 18), failCase("only-a")},
	}
	b := &runner.SuiteResult{
		Suite: "s", AgentLabel: "agent-B", Totals: runner.Totals{Pass: 1},
		Cases: []*runner.CaseResult{passCase("shared", 20), passCase("only-b", 5)},
	}
	var buf bytes.Buffer
	Compare(&buf, []*runner.SuiteResult{a, b})
	out := buf.String()
	for _, want := range []string{"Case", "agent-A", "agent-B", "shared", "only-a", "only-b", "TOTAL"} {
		if !strings.Contains(out, want) {
			t.Errorf("Compare output missing %q\n---\n%s", want, out)
		}
	}
	// only-a is absent from run B → a "-" placeholder must appear.
	if !strings.Contains(out, "-") {
		t.Errorf("Compare should mark missing cases with '-'\n%s", out)
	}
}

func TestDiffRegressionsAndFixes(t *testing.T) {
	base := &runner.SuiteResult{
		Suite: "s", StartedAt: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC),
		Cases: []*runner.CaseResult{
			passCase("stable", 10),
			passCase("regressing", 10),
			failCase("fixing"),
			passCase("gone", 10),
		},
	}
	cur := &runner.SuiteResult{
		Suite: "s", StartedAt: time.Date(2026, 7, 14, 11, 0, 0, 0, time.UTC),
		Cases: []*runner.CaseResult{
			passCase("stable", 12),
			failCase("regressing"),
			passCase("fixing", 10),
			passCase("added", 10),
		},
	}
	var buf bytes.Buffer
	regressions := Diff(&buf, base, cur)
	if regressions != 1 {
		t.Errorf("regressions = %d, want 1", regressions)
	}
	out := buf.String()
	for _, want := range []string{"REGRESSED", "regressing", "FIXED", "fixing", "added", "(new", "gone", "removed", "avg latency", "total tokens"} {
		if !strings.Contains(out, want) {
			t.Errorf("Diff output missing %q\n---\n%s", want, out)
		}
	}
}

func TestSaveAndLoadBaseline(t *testing.T) {
	tmp := t.TempDir()
	old := benchHome
	benchHome = func() string { return tmp }
	defer func() { benchHome = old }()

	sr := sampleSuite()
	path, err := SaveRun(sr)
	if err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	if !fileExists(path) {
		t.Fatalf("timestamped run file not written: %s", path)
	}

	// "last" resolves to latest.json.
	loaded, err := LoadBaseline("demo", "last")
	if err != nil {
		t.Fatalf("LoadBaseline last: %v", err)
	}
	if loaded.Suite != "demo" || len(loaded.Cases) != 4 {
		t.Errorf("loaded baseline mismatch: %+v", loaded)
	}

	// A ref that is a file path loads that file directly.
	loaded2, err := LoadBaseline("demo", path)
	if err != nil {
		t.Fatalf("LoadBaseline path: %v", err)
	}
	if loaded2.Totals != sr.Totals {
		t.Errorf("loaded-by-path totals = %+v", loaded2.Totals)
	}

	if _, err := LoadBaseline("demo", "/no/such/file.json"); err == nil {
		t.Error("expected error loading a missing baseline")
	}
}

func TestSaveRunUsesInjectedDir(t *testing.T) {
	tmp := t.TempDir()
	old := benchHome
	benchHome = func() string { return tmp }
	defer func() { benchHome = old }()

	if _, err := SaveRun(sampleSuite()); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	// The real home dir must be untouched: everything lands under tmp.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "demo" {
		t.Errorf("history not written under injected dir: %+v", entries)
	}
}
