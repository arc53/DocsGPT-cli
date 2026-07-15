package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDurationUnmarshal(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{`90`, 90 * time.Second, false},
		{`1.5`, 1500 * time.Millisecond, false},
		{`"90s"`, 90 * time.Second, false},
		{`"2m"`, 2 * time.Minute, false},
		{`"nope"`, 0, true},
		{`[1]`, 0, true},
	}
	for _, tc := range cases {
		var d Duration
		err := yaml.Unmarshal([]byte(tc.in), &d)
		if tc.err != (err != nil) {
			t.Errorf("%s: err = %v, want err=%v", tc.in, err, tc.err)
			continue
		}
		if !tc.err && d.Std() != tc.want {
			t.Errorf("%s: got %v, want %v", tc.in, d.Std(), tc.want)
		}
	}
}

func TestStringListUnmarshal(t *testing.T) {
	var s StringList
	if err := yaml.Unmarshal([]byte(`"one"`), &s); err != nil || len(s) != 1 || s[0] != "one" {
		t.Errorf("single string: got %v (%v)", s, err)
	}
	if err := yaml.Unmarshal([]byte(`[a, b]`), &s); err != nil || len(s) != 2 || s[1] != "b" {
		t.Errorf("list: got %v (%v)", s, err)
	}
}

func writeCase(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, CaseFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const minimalCase = `
description: "smoke"
question: "hi"
expect:
  answer:
    contains: "hello"
`

func TestLoadSuite(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, SuiteFileName), []byte("agent: my-agent\ntimeout: 30s\n"), 0o644)
	writeCase(t, root, "01-a", minimalCase)
	writeCase(t, root, "nested/02-b", minimalCase)
	// Hidden dirs and case-less dirs are ignored.
	writeCase(t, root, ".hidden/03-c", minimalCase)
	os.MkdirAll(filepath.Join(root, "empty"), 0o755)

	s, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Cases) != 2 {
		t.Fatalf("got %d cases, want 2: %+v", len(s.Cases), s.Cases)
	}
	if s.Cases[0].Name != "01-a" || s.Cases[1].Name != "nested/02-b" {
		t.Errorf("case names: %q, %q", s.Cases[0].Name, s.Cases[1].Name)
	}
	if s.Config.Agent != "my-agent" || s.Config.Timeout.Std() != 30*time.Second {
		t.Errorf("suite config not loaded: %+v", s.Config)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	writeCase(t, root, "01-typo", strings.Replace(minimalCase, "question:", "quesiton:", 1))
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "quesiton") {
		t.Errorf("want unknown-field error, got %v", err)
	}
}

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name, caseYAML, wantErr string
	}{
		{"no question", "description: x\nexpect:\n  answer: {contains: hi}\n", "question is required"},
		{"no expect", "question: hi\n", "no assertion sections"},
		{"bad target", "question: hi\ntarget: grpc\nexpect:\n  answer: {contains: hi}\n", "unknown target"},
		{"webhook attachments", "question: hi\ntarget: webhook\nwebhook_url: http://x\nattachments: [a.txt]\nexpect:\n  answer: {contains: hi}\n", "not supported by the webhook target"},
		{"judge no rubric", "question: hi\nexpect:\n  judge: {min_score: 0.5}\n", "requires a rubric"},
		{"bad regex", "question: hi\nexpect:\n  answer: {regex: '['}\n", "bad regex"},
		{"missing attachment", "question: hi\nattachments: [nope.pdf]\nexpect:\n  answer: {contains: hi}\n", "nope.pdf"},
	}
	for _, tc := range tests {
		root := t.TempDir()
		writeCase(t, root, "01-case", tc.caseYAML)
		_, err := Load(root)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: got %v, want substring %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestSkippedCaseNeedsNoQuestion(t *testing.T) {
	root := t.TempDir()
	writeCase(t, root, "01-skip", "skip: flaky upstream\n")
	s, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Cases[0].Skip != "flaky upstream" {
		t.Errorf("skip not loaded: %+v", s.Cases[0])
	}
}

func TestEffectivePrecedence(t *testing.T) {
	s := &Suite{Config: SuiteConfig{
		Agent: "suite-agent", Target: TargetStream, Timeout: Duration(30 * time.Second),
		Repeat: 3, MinPass: 2,
	}}
	c := &Case{Agent: "case-agent", Timeout: Duration(10 * time.Second)}
	eff := s.Effective(c)
	if eff.Agent != "case-agent" || eff.Target != TargetStream ||
		eff.Timeout != 10*time.Second || eff.Repeat != 3 || eff.MinPass != 2 {
		t.Errorf("unexpected effective: %+v", eff)
	}

	// Defaults with empty config everywhere.
	eff = (&Suite{}).Effective(&Case{})
	if eff.Target != TargetV1 || eff.Timeout != DefaultTimeout ||
		eff.PollInterval != DefaultPollInterval || eff.Repeat != 1 || eff.MinPass != 1 {
		t.Errorf("unexpected defaults: %+v", eff)
	}

	// min_pass is clamped to repeat.
	eff = (&Suite{Config: SuiteConfig{MinPass: 5}}).Effective(&Case{Repeat: 2})
	if eff.MinPass != 2 {
		t.Errorf("min_pass not clamped: %+v", eff)
	}
}

func TestFilter(t *testing.T) {
	s := &Suite{Cases: []*Case{
		{Name: "01-offshore", Description: "checks offshore fees", Tags: StringList{"smoke"}},
		{Name: "02-currency", Description: "currency mismatch", Tags: StringList{"llm"}},
	}}
	if got := s.Filter("offshore", nil); len(got) != 1 || got[0].Name != "01-offshore" {
		t.Errorf("name filter: %+v", got)
	}
	if got := s.Filter("mismatch", nil); len(got) != 1 || got[0].Name != "02-currency" {
		t.Errorf("description filter: %+v", got)
	}
	if got := s.Filter("", []string{"SMOKE"}); len(got) != 1 || got[0].Name != "01-offshore" {
		t.Errorf("tag filter: %+v", got)
	}
	if got := s.Filter("", nil); len(got) != 2 {
		t.Errorf("no filter: %+v", got)
	}
}

func TestGoldenRoundtrip(t *testing.T) {
	c := &Case{Dir: t.TempDir()}
	if g, err := c.LoadGolden(); err != nil || g != nil {
		t.Fatalf("absent golden: %v %v", g, err)
	}
	want := &Golden{Answer: "42  "}
	if err := c.SaveGolden(want); err != nil {
		t.Fatal(err)
	}
	got, err := c.LoadGolden()
	if err != nil || got == nil || got.Answer != want.Answer {
		t.Fatalf("roundtrip: %+v, %v", got, err)
	}
}

func TestLoadRejectsNegativeDurations(t *testing.T) {
	root := t.TempDir()
	writeCase(t, root, "01-neg", "question: hi\ntimeout: -5\nexpect:\n  answer: {contains: hi}\n")
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "timeout must be positive") {
		t.Errorf("negative case timeout: got %v", err)
	}

	root2 := t.TempDir()
	os.WriteFile(filepath.Join(root2, SuiteFileName), []byte("poll_interval: -1s\n"), 0o644)
	writeCase(t, root2, "01-ok", minimalCase)
	if _, err := Load(root2); err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Errorf("negative suite poll_interval: got %v", err)
	}
}

func TestLoadRejectsOutOfRangeMinScore(t *testing.T) {
	root := t.TempDir()
	writeCase(t, root, "01-judge", "question: hi\nexpect:\n  judge: {rubric: r, min_score: 1.5}\n")
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "min_score") {
		t.Errorf("min_score 1.5: got %v", err)
	}
}

func TestStringListDropsEmptyEntries(t *testing.T) {
	var s StringList
	if err := yaml.Unmarshal([]byte(`["a", "", "b"]`), &s); err != nil || len(s) != 2 {
		t.Errorf("empties not dropped: %v (%v)", s, err)
	}
	if err := yaml.Unmarshal([]byte(`""`), &s); err != nil || len(s) != 0 {
		t.Errorf("single empty not dropped: %v (%v)", s, err)
	}
}
