package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"docsgpt-cli/internal/bench/assert"
	"docsgpt-cli/internal/bench/judge"
	"docsgpt-cli/internal/bench/spec"
	"docsgpt-cli/internal/bench/target"
)

// --- test doubles ----------------------------------------------------------

// scriptTarget records every request and returns a scripted result per call.
type scriptTarget struct {
	mu   sync.Mutex
	reqs []target.Request
	fn   func(call int, req target.Request) (*target.Result, error)
}

func (s *scriptTarget) Name() string { return "fake" }

func (s *scriptTarget) Run(_ context.Context, req target.Request) (*target.Result, error) {
	s.mu.Lock()
	call := len(s.reqs)
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	return s.fn(call, req)
}

func (s *scriptTarget) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

// withTarget installs a fake target for the duration of a test.
func withTarget(t *testing.T, tg target.Target) {
	t.Helper()
	old := lookup
	lookup = func(string) (target.Target, error) { return tg, nil }
	t.Cleanup(func() { lookup = old })
}

func withJudge(t *testing.T, fn func(ctx context.Context, cfg judge.Config, q, a, r string) (*judge.Verdict, error)) {
	t.Helper()
	old := judgeRun
	judgeRun = fn
	t.Cleanup(func() { judgeRun = old })
}

// answerResult builds a minimal successful target result.
func answerResult(answer string) *target.Result {
	return &target.Result{Answer: answer, Latency: 5 * time.Millisecond}
}

// defaultResolver echoes a key value derived from the reference.
func defaultResolver(ref string) (string, string, error) {
	if ref == "" {
		return "", "", fmt.Errorf("empty")
	}
	return "KEY:" + ref, ref, nil
}

// containsCase builds a case expecting the answer to contain sub.
func containsCase(name, sub string) *spec.Case {
	return &spec.Case{
		Name:     name,
		Dir:      name, // no golden here
		Question: "q?",
		Expect:   spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{sub}}},
	}
}

func newSuite(cfg spec.SuiteConfig, cases ...*spec.Case) *spec.Suite {
	return &spec.Suite{Name: "t", Dir: "/tmp/t", Config: cfg, Cases: cases}
}

// --- tests -----------------------------------------------------------------

func TestRunBasicPassFail(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(_ int, req target.Request) (*target.Result, error) {
		if req.Question == "pass?" {
			return answerResult("this contains ok yes"), nil
		}
		return answerResult("nope"), nil
	}})

	pass := &spec.Case{Name: "p", Dir: "p", Question: "pass?", Expect: spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"ok"}}}}
	fail := &spec.Case{Name: "f", Dir: "f", Question: "fail?", Expect: spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"ok"}}}}

	suite := newSuite(spec.SuiteConfig{Agent: "a"}, pass, fail)
	sr, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sr.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d", sr.SchemaVersion)
	}
	if sr.Totals.Pass != 1 || sr.Totals.Fail != 1 {
		t.Errorf("totals = %+v, want 1 pass 1 fail", sr.Totals)
	}
	if sr.AgentLabel != "a" {
		t.Errorf("AgentLabel = %q, want a", sr.AgentLabel)
	}
	if sr.Target != "v1" {
		t.Errorf("Target = %q, want v1 (default)", sr.Target)
	}
}

func TestRunSkipNeverCallsTarget(t *testing.T) {
	tg := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("x"), nil }}
	withTarget(t, tg)

	skip := &spec.Case{Name: "s", Dir: "s", Skip: "not ready", Question: "q?", Expect: spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"x"}}}}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, skip)
	sr, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tg.calls() != 0 {
		t.Errorf("skip case contacted the target %d times", tg.calls())
	}
	if sr.Totals.Skip != 1 || sr.Cases[0].Status != StatusSkip {
		t.Errorf("expected skip, got %+v / %s", sr.Totals, sr.Cases[0].Status)
	}
	if sr.Cases[0].SkipReason != "not ready" {
		t.Errorf("SkipReason = %q", sr.Cases[0].SkipReason)
	}
}

func TestRunRepeatEarlyStopPass(t *testing.T) {
	// Repeat 5, min_pass 2, every run passes → should stop after 2 runs.
	tg := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("ok"), nil }}
	withTarget(t, tg)

	c := containsCase("c", "ok")
	c.Repeat = 5
	c.MinPass = 2
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tg.calls() != 2 {
		t.Errorf("target calls = %d, want 2 (early stop on min-pass)", tg.calls())
	}
	if sr.Cases[0].Status != StatusPass || sr.Cases[0].PassedRuns != 2 {
		t.Errorf("case = %s passed=%d, want pass/2", sr.Cases[0].Status, sr.Cases[0].PassedRuns)
	}
}

func TestRunRepeatEarlyStopFail(t *testing.T) {
	// Repeat 5, min_pass 4 (allowed failures = 1), every run fails → stop after 2.
	tg := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("nope"), nil }}
	withTarget(t, tg)

	c := containsCase("c", "ok")
	c.Repeat = 5
	c.MinPass = 4
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tg.calls() != 2 {
		t.Errorf("target calls = %d, want 2 (early stop once min-pass unreachable)", tg.calls())
	}
	if sr.Cases[0].Status != StatusFail {
		t.Errorf("status = %s, want fail", sr.Cases[0].Status)
	}
}

func TestRunFailFast(t *testing.T) {
	tg := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("nope"), nil }}
	withTarget(t, tg)

	suite := newSuite(spec.SuiteConfig{Agent: "a"},
		containsCase("a", "ok"), containsCase("b", "ok"), containsCase("c", "ok"))
	sr, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x",
		Concurrency: 1, FailFast: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sr.Cases) != 1 {
		t.Errorf("fail-fast should have run 1 case, ran %d", len(sr.Cases))
	}
	if sr.Cases[0].Status != StatusFail {
		t.Errorf("first case status = %s", sr.Cases[0].Status)
	}
}

func TestRunTargetErrorIsErrorStatus(t *testing.T) {
	tg := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) {
		return nil, fmt.Errorf("boom")
	}}
	withTarget(t, tg)

	suite := newSuite(spec.SuiteConfig{Agent: "a"}, containsCase("c", "ok"))
	sr, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sr.Cases[0].Status != StatusError || sr.Totals.Error != 1 {
		t.Errorf("expected error status, got %s / %+v", sr.Cases[0].Status, sr.Totals)
	}
	if got := sr.Cases[0].Runs[0].Error; got == "" {
		t.Error("errored run should carry the error text")
	}
}

func TestRunGoldenRecord(t *testing.T) {
	dir := t.TempDir()
	tg := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) {
		return answerResult("the recorded answer"), nil
	}}
	withTarget(t, tg)

	// Use a plain assertion (not golden:true) so the informational run passes;
	// golden:true would fail during record since the golden is written after
	// assertions evaluate.
	c := containsCase("g", "recorded")
	c.Dir = dir
	c.Repeat = 3 // record mode must ignore repeat
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)

	var goldenEvents []string
	sr, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x",
		UpdateGolden: true,
		OnEvent: func(e Event) {
			if e.Type == EventGolden {
				goldenEvents = append(goldenEvents, e.Msg)
			}
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tg.calls() != 1 {
		t.Errorf("record mode made %d calls, want 1", tg.calls())
	}
	g, err := c.LoadGolden()
	if err != nil || g == nil {
		t.Fatalf("golden not written: %v", err)
	}
	if g.Answer != "the recorded answer" {
		t.Errorf("golden answer = %q", g.Answer)
	}
	if len(goldenEvents) != 1 || goldenEvents[0] != c.GoldenPath() {
		t.Errorf("golden events = %v", goldenEvents)
	}
	// The case's contains assertion matches the recorded answer, so the run passes.
	if sr.Cases[0].Status != StatusPass {
		t.Errorf("record case status = %s, want pass", sr.Cases[0].Status)
	}
}

func TestRunJudgeMissingConfig(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(int, target.Request) (*target.Result, error) {
		return answerResult("some answer"), nil
	}})

	c := &spec.Case{Name: "j", Dir: "j", Question: "q?",
		Expect: spec.Expect{Judge: &spec.JudgeExpect{Rubric: "is it good", MinScore: fptr(0.7)}}}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x",
		Judge: nil, // no judge configured
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sr.Cases[0].Status != StatusFail {
		t.Fatalf("case status = %s, want fail", sr.Cases[0].Status)
	}
	var found *assert.Result
	for i, a := range sr.Cases[0].Runs[0].Assertions {
		if a.Name == "judge" {
			found = &sr.Cases[0].Runs[0].Assertions[i]
		}
	}
	if found == nil || found.Status != assert.StatusFail {
		t.Fatalf("expected a failing judge assertion, got %+v", sr.Cases[0].Runs[0].Assertions)
	}
	if found.Message == "" {
		t.Error("judge failure should explain the missing config")
	}
}

func TestRunJudgePasses(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(int, target.Request) (*target.Result, error) {
		return answerResult("a decent answer"), nil
	}})
	var gotQ, gotA, gotRubric string
	withJudge(t, func(_ context.Context, cfg judge.Config, q, a, r string) (*judge.Verdict, error) {
		gotQ, gotA, gotRubric = q, a, r
		if cfg.APIKey != "KEY:judge-agent" {
			t.Errorf("judge api key = %q", cfg.APIKey)
		}
		return &judge.Verdict{Score: 0.9, Reasoning: "solid"}, nil
	})

	c := &spec.Case{Name: "j", Dir: "j", Question: "the question",
		Expect: spec.Expect{Judge: &spec.JudgeExpect{Rubric: "the rubric", MinScore: fptr(0.7)}}}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://base",
		Judge: &spec.JudgeAgent{Agent: "judge-agent"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sr.Cases[0].Status != StatusPass {
		t.Errorf("status = %s, want pass", sr.Cases[0].Status)
	}
	if gotQ != "the question" || gotA != "a decent answer" || gotRubric != "the rubric" {
		t.Errorf("judge received q=%q a=%q rubric=%q", gotQ, gotA, gotRubric)
	}
}

func TestRunURLAndAgentPrecedence(t *testing.T) {
	tg := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("ok"), nil }}
	withTarget(t, tg)

	c := containsCase("c", "ok")
	c.BaseURL = "http://case"
	c.Agent = "case-agent"
	suite := newSuite(spec.SuiteConfig{Agent: "suite-agent", BaseURL: "http://suite"}, c)

	// URLOverride and AgentOverride win over everything.
	_, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver,
		BaseURL: "http://config", URLOverride: "http://override", AgentOverride: "override-agent",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := tg.reqs[0].BaseURL; got != "http://override" {
		t.Errorf("BaseURL = %q, want override", got)
	}
	if got := tg.reqs[0].APIKey; got != "KEY:override-agent" {
		t.Errorf("APIKey = %q, want KEY:override-agent", got)
	}

	// Without overrides, the case values win over the suite values.
	tg2 := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("ok"), nil }}
	withTarget(t, tg2)
	if _, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://config",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := tg2.reqs[0].BaseURL; got != "http://case" {
		t.Errorf("BaseURL = %q, want case", got)
	}
	if got := tg2.reqs[0].APIKey; got != "KEY:case-agent" {
		t.Errorf("APIKey = %q, want KEY:case-agent", got)
	}
}

func TestRunNoAgentConfigured(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("ok"), nil }})
	c := containsCase("c", "ok")
	suite := newSuite(spec.SuiteConfig{}, c) // no agent anywhere
	sr, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sr.Cases[0].Status != StatusError {
		t.Errorf("status = %s, want error (no agent)", sr.Cases[0].Status)
	}
}

func TestRunConcurrencyExceedsCases(t *testing.T) {
	var active, maxActive atomic.Int32
	tg := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) {
		n := active.Add(1)
		for {
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		active.Add(-1)
		return answerResult("ok"), nil
	}}
	withTarget(t, tg)

	suite := newSuite(spec.SuiteConfig{Agent: "a"}, containsCase("a", "ok"), containsCase("b", "ok"))
	sr, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x",
		Concurrency: 5, // more than the 2 cases
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sr.Totals.Pass != 2 {
		t.Errorf("expected 2 passes, got %+v", sr.Totals)
	}
	if maxActive.Load() < 2 {
		t.Errorf("cases did not run concurrently (max active = %d)", maxActive.Load())
	}
}

func TestRunAttachmentsUploadedOncePerCase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := uploadAttachments
	var uploads atomic.Int32
	uploadAttachments = func(_ context.Context, _, _ string, paths []string, _ time.Duration) ([]string, error) {
		uploads.Add(1)
		return []string{"att-1"}, nil
	}
	t.Cleanup(func() { uploadAttachments = old })

	tg := &scriptTarget{fn: func(_ int, req target.Request) (*target.Result, error) {
		if len(req.AttachmentIDs) != 1 || req.AttachmentIDs[0] != "att-1" {
			t.Errorf("attachment ids not threaded to run: %v", req.AttachmentIDs)
		}
		return answerResult("ok"), nil
	}}
	withTarget(t, tg)

	c := containsCase("c", "ok")
	c.Dir = dir
	c.Attachments = spec.StringList{"a.txt"}
	c.Repeat = 3
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	if _, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if uploads.Load() != 1 {
		t.Errorf("attachments uploaded %d times, want once per case", uploads.Load())
	}
	if tg.calls() != 3 {
		t.Errorf("expected 3 runs, got %d", tg.calls())
	}
}

func TestRunEmptyCasesError(t *testing.T) {
	if _, err := Run(context.Background(), Options{Suite: newSuite(spec.SuiteConfig{}), Cases: nil, ResolveKey: defaultResolver}); err == nil {
		t.Error("expected error for empty cases")
	}
}

func fptr(f float64) *float64 { return &f }

func TestRunRevalidatesTargetOverride(t *testing.T) {
	// A --target webhook override must not bypass the loader's webhook
	// constraints (no attachments; webhook_url required).
	c := &spec.Case{Name: "c", Dir: t.TempDir(), Question: "q?", Target: spec.TargetV1,
		Expect: spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"x"}}}}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)

	sr, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver,
		BaseURL: "http://x", TargetOverride: spec.TargetWebhook,
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := sr.Cases[0]
	if cr.Status != StatusError || !strings.Contains(cr.Runs[0].Error, "webhook_url") {
		t.Errorf("missing webhook_url not caught: %+v", cr)
	}
}

func TestWebhookURLOverride(t *testing.T) {
	st := &scriptTarget{fn: func(_ int, req target.Request) (*target.Result, error) {
		if req.WebhookURL != "https://cli/hook/tok" {
			return nil, fmt.Errorf("webhook url = %q", req.WebhookURL)
		}
		return answerResult("ok fine"), nil
	}}
	withTarget(t, st)

	// Case declares webhook target but no URL anywhere in YAML — the CLI
	// override must satisfy the runner's validation and reach the target.
	c := &spec.Case{Name: "w", Dir: "w", Question: "q?", Target: spec.TargetWebhook,
		Expect: spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"ok"}}}}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)

	sr, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver,
		BaseURL: "http://x", WebhookURLOverride: "https://cli/hook/tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sr.Cases[0].Status != StatusPass {
		t.Errorf("override not applied: %+v", sr.Cases[0].Runs[0])
	}

	// Without the override the same case must error clearly.
	sr, err = Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sr.Cases[0].Status != StatusError || !strings.Contains(sr.Cases[0].Runs[0].Error, "--webhook-url") {
		t.Errorf("missing url should error mentioning --webhook-url: %+v", sr.Cases[0].Runs[0])
	}
}
