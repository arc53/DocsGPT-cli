package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the bench-plan additions: model, turns, answer target, negative
// cases (expect.error), stream integrity, TTFT limits, attachment modes.

func TestLoadTurnsAndModel(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, SuiteFileName), []byte("model: suite-model\ntarget: stream\nrun_tag: nightly\npricing:\n  m1: {input_per_million: 1, output_per_million: 2}\n"), 0o644)
	writeCase(t, root, "01-turns", `
turns:
  - question: "My project is Zephyr."
    expect: {answer: {contains: Zephyr}}
  - question: "What is my project called?"
expect:
  answer: {contains: Zephyr}
  limits: {max_first_token_seconds: 5}
`)
	writeCase(t, root, "02-model", "model: case-model\nquestion: hi\nexpect:\n  answer: {contains: hi}\n")
	s, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Cases[0]
	if !c.MultiTurn() || len(c.Questions()) != 2 || c.FinalQuestion() != "What is my project called?" {
		t.Errorf("turns not loaded: %+v", c)
	}
	if c.Turns[0].Expect == nil || c.Turns[0].Expect.Answer == nil {
		t.Errorf("per-turn expect not loaded")
	}
	if got := s.Effective(c).Model; got != "suite-model" {
		t.Errorf("suite model = %q", got)
	}
	if got := s.Effective(s.Cases[1]).Model; got != "case-model" {
		t.Errorf("case model = %q", got)
	}
	if s.Config.RunTag != "nightly" || s.Config.Pricing["m1"].OutputPerMillion != 2 {
		t.Errorf("suite config: %+v", s.Config)
	}
}

func TestLoadTurnOnlyExpects(t *testing.T) {
	root := t.TempDir()
	writeCase(t, root, "01-t", "turns:\n  - question: a\n    expect: {answer: {contains: a}}\n  - question: b\n")
	if _, err := Load(root); err != nil {
		t.Fatalf("turn-level expects should satisfy the assertion requirement: %v", err)
	}
}

func TestLoadV2Validation(t *testing.T) {
	tests := []struct {
		name, suiteYAML, caseYAML, wantErr string
	}{
		{"question and turns", "", "question: hi\nturns: [{question: a}]\nexpect: {answer: {contains: hi}}\n", "mutually exclusive"},
		{"empty turn question", "", "turns: [{question: ''}]\nexpect: {answer: {contains: hi}}\n", "turns[0].question is required"},
		{"turns on webhook", "target: webhook\n", "turns: [{question: a}]\nexpect: {answer: {contains: hi}}\n", "not supported by the webhook target"},
		{"error on intermediate turn", "", "turns:\n  - question: a\n    expect: {error: {status: 400}}\n  - question: b\nexpect: {answer: {contains: b}}\n", "only allowed on the last turn"},
		{"error mixed with answer", "", "question: hi\nexpect:\n  error: {status: 400}\n  answer: {contains: hi}\n", "cannot be combined"},
		{"error bad status", "", "question: hi\nexpect:\n  error: {status: 12}\n", "must be an HTTP status"},
		{"stream on v1", "", "question: hi\nexpect:\n  stream: {end_frame: true}\n", "requires target stream"},
		{"stream bad thought", "target: stream\n", "question: hi\nexpect:\n  stream: {thought: maybe}\n", "thought must be"},
		{"stream error_frame true", "target: stream\n", "question: hi\nexpect:\n  stream: {error_frame: true}\n", "use expect.error"},
		{"inline on stream", "target: stream\n", "question: hi\nattachments_mode: inline\nexpect: {answer: {contains: hi}}\n", "requires target v1"},
		{"bad attachments_mode", "", "question: hi\nattachments_mode: teleport\nexpect: {answer: {contains: hi}}\n", "unknown attachments_mode"},
		{"answer target attachments", "target: answer\n", "question: hi\nattachments: [a.txt]\nexpect: {answer: {contains: hi}}\n", "not supported by the answer target"},
		{"negative limit", "", "question: hi\nexpect:\n  limits: {max_first_token_seconds: -1}\n", "must be positive"},
	}
	for _, tc := range tests {
		root := t.TempDir()
		if tc.suiteYAML != "" {
			os.WriteFile(filepath.Join(root, SuiteFileName), []byte(tc.suiteYAML), 0o644)
		}
		writeCase(t, root, "01-case", tc.caseYAML)
		_, err := Load(root)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: got %v, want substring %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestLoadNegativeAndStreamCases(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, SuiteFileName), []byte("target: stream\n"), 0o644)
	writeCase(t, root, "01-neg", "question: hi\nexpect:\n  error: {status: 401, contains: Invalid}\n  limits: {max_seconds: 10}\n")
	writeCase(t, root, "02-neg-any", "question: hi\nexpect:\n  error: {}\n")
	writeCase(t, root, "03-stream", "question: hi\nexpect:\n  stream:\n    end_frame: true\n    error_frame: false\n    thought: present\n    frames_contain: [source, message_id]\n")
	writeCase(t, root, "04-answer", "target: answer\nquestion: hi\nexpect: {answer: {contains: hi}}\n")
	writeCase(t, root, "05-inline", "target: v1\nattachments_mode: inline\nstream: true\nattachments: [f.txt]\nquestion: hi\nexpect: {answer: {contains: hi}}\n")
	os.WriteFile(filepath.Join(root, "05-inline", "f.txt"), []byte("x"), 0o644)
	s, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if e := s.Cases[0].Expect.Error; e == nil || e.Status != 401 || len(e.Contains) != 1 {
		t.Errorf("error expect: %+v", e)
	}
	if s.Cases[1].Expect.Error == nil {
		t.Errorf("bare `error: {}` should produce a non-nil ErrorExpect")
	}
	if st := s.Cases[2].Expect.Stream; st == nil || st.EndFrame == nil || !*st.EndFrame || st.Thought != ThoughtPresent || len(st.FramesContain) != 2 {
		t.Errorf("stream expect: %+v", st)
	}
	if eff := s.Effective(s.Cases[3]); eff.Target != TargetAnswer {
		t.Errorf("answer target: %+v", eff)
	}
	if eff := s.Effective(s.Cases[4]); eff.Target != TargetV1 || !eff.Stream || eff.AttachmentsMode != AttachmentsInline {
		t.Errorf("inline/stream effective: %+v", eff)
	}
}

func TestEffectiveStreamAndAttachmentsMode(t *testing.T) {
	tr, fa := true, false
	s := &Suite{Config: SuiteConfig{Stream: &tr, AttachmentsMode: AttachmentsInline}}
	if eff := s.Effective(&Case{}); !eff.Stream || eff.AttachmentsMode != AttachmentsInline {
		t.Errorf("suite defaults not applied: %+v", eff)
	}
	if eff := s.Effective(&Case{Stream: &fa, AttachmentsMode: AttachmentsUpload}); eff.Stream || eff.AttachmentsMode != AttachmentsUpload {
		t.Errorf("case override not applied: %+v", eff)
	}
	if eff := (&Suite{}).Effective(&Case{}); eff.Stream || eff.AttachmentsMode != AttachmentsUpload || eff.Model != "" {
		t.Errorf("defaults: %+v", eff)
	}
}

func TestValidTarget(t *testing.T) {
	for _, n := range AllTargets {
		if !ValidTarget(n) {
			t.Errorf("%s should be valid", n)
		}
	}
	if ValidTarget("grpc") {
		t.Errorf("grpc should be invalid")
	}
}
