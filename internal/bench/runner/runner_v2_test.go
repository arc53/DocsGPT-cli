package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"docsgpt-cli/internal/bench/assert"
	"docsgpt-cli/internal/bench/judge"
	"docsgpt-cli/internal/bench/pricing"
	"docsgpt-cli/internal/bench/spec"
	"docsgpt-cli/internal/bench/target"
)

// Runner tests never touch the network for pricing: /api/models is stubbed
// with a fixed catalog so cost assertions are deterministic.
func TestMain(m *testing.M) {
	fetchPricing = func(_ context.Context, t *pricing.Table, _ string) error {
		t.Merge(map[string]spec.ModelPricing{"priced-model": {InputPerMillion: 1, OutputPerMillion: 10}})
		return nil
	}
	os.Exit(m.Run())
}

func TestRunModelPrecedenceAndStamping(t *testing.T) {
	tg := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("ok"), nil }}
	withTarget(t, tg)

	suiteCase := containsCase("suite", "ok")
	caseCase := containsCase("case", "ok")
	caseCase.Model = "case-model"
	suite := newSuite(spec.SuiteConfig{Agent: "a", Model: "suite-model"}, suiteCase, caseCase)

	sr, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	if tg.reqs[0].Model != "suite-model" || tg.reqs[1].Model != "case-model" {
		t.Errorf("request models = %q, %q", tg.reqs[0].Model, tg.reqs[1].Model)
	}
	if sr.Cases[0].Model != "suite-model" || sr.Cases[1].Model != "case-model" || sr.Model != "mixed" {
		t.Errorf("stamped models: %q %q suite=%q", sr.Cases[0].Model, sr.Cases[1].Model, sr.Model)
	}

	// --model wins over everything and the suite result carries it.
	tg.reqs = nil
	sr, err = Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x", ModelOverride: "flag-model"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tg.reqs {
		if r.Model != "flag-model" {
			t.Errorf("override not applied: %q", r.Model)
		}
	}
	if sr.Model != "flag-model" || sr.Cases[1].Model != "flag-model" {
		t.Errorf("suite model = %q case model = %q", sr.Model, sr.Cases[1].Model)
	}
}

func TestRunPassesStreamRunTagAndInlineFiles(t *testing.T) {
	tg := &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("ok"), nil }}
	withTarget(t, tg)
	uploads := 0
	old := uploadAttachments
	uploadAttachments = func(context.Context, string, string, []string, time.Duration) ([]string, error) {
		uploads++
		return []string{"id"}, nil
	}
	t.Cleanup(func() { uploadAttachments = old })

	tr := true
	c := containsCase("inline", "ok")
	c.Dir = t.TempDir()
	os.WriteFile(c.Dir+"/f.pdf", []byte("x"), 0o644)
	c.Attachments = spec.StringList{"f.pdf"}
	c.AttachmentsMode = spec.AttachmentsInline
	c.Stream = &tr
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	_, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x", RunTag: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	req := tg.reqs[0]
	if uploads != 0 || len(req.AttachmentIDs) != 0 {
		t.Errorf("inline mode must not upload: uploads=%d ids=%v", uploads, req.AttachmentIDs)
	}
	if len(req.InlineFiles) != 1 || req.InlineFiles[0].Name != "f.pdf" || !strings.HasSuffix(req.InlineFiles[0].Path, "/f.pdf") {
		t.Errorf("InlineFiles = %+v", req.InlineFiles)
	}
	if !req.Stream || req.RunTag != "ci" {
		t.Errorf("Stream=%v RunTag=%q", req.Stream, req.RunTag)
	}
}

func TestRunInlineRequiresV1(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("ok"), nil }})
	c := containsCase("inline", "ok")
	c.Attachments = spec.StringList{"f.pdf"}
	c.AttachmentsMode = spec.AttachmentsInline
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, _ := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x", TargetOverride: spec.TargetStream})
	if sr.Cases[0].Status != StatusError || !strings.Contains(sr.Cases[0].Runs[0].Error, "inline") {
		t.Errorf("expected inline/v1 error, got %+v", sr.Cases[0].Runs[0])
	}
}

func TestRunAnswerTargetRejectsAttachments(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("ok"), nil }})
	c := containsCase("att", "ok")
	c.Attachments = spec.StringList{"f.pdf"}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, _ := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x", TargetOverride: spec.TargetAnswer})
	if sr.Cases[0].Status != StatusError || !strings.Contains(sr.Cases[0].Runs[0].Error, "answer target") {
		t.Errorf("expected answer-target attachment error, got %+v", sr.Cases[0].Runs[0])
	}
}

func TestRunMultiTurnConversationCarry(t *testing.T) {
	// A conversation-carrying target (stream-like): returns a conversation id
	// on turn 1 and expects it back on turn 2. History must be offered too so
	// stateless targets can replay it.
	tg := &scriptTarget{fn: func(call int, req target.Request) (*target.Result, error) {
		switch call {
		case 0:
			if req.ConversationID != "" || len(req.History) != 0 {
				return nil, fmt.Errorf("turn 1 must start fresh: %+v", req)
			}
			return &target.Result{Answer: "Noted, Zephyr.", ConversationID: "conv-1", Latency: 100 * time.Millisecond, FirstOutput: 20 * time.Millisecond, Usage: &target.Usage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10}}, nil
		case 1:
			if req.ConversationID != "conv-1" {
				return nil, fmt.Errorf("turn 2 lost the conversation id: %+v", req)
			}
			if len(req.History) != 1 || req.History[0].Answer != "Noted, Zephyr." {
				return nil, fmt.Errorf("turn 2 history wrong: %+v", req.History)
			}
			if req.Question != "What is my project?" {
				return nil, fmt.Errorf("turn 2 question = %q", req.Question)
			}
			return &target.Result{Answer: "Your project is Zephyr.", ConversationID: "conv-1", Latency: 200 * time.Millisecond, FirstOutput: 50 * time.Millisecond, Usage: &target.Usage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25}}, nil
		}
		return nil, fmt.Errorf("unexpected call %d", call)
	}}
	withTarget(t, tg)

	c := &spec.Case{Name: "mt", Dir: "mt",
		Turns: []spec.Turn{
			{Question: "My project is Zephyr.", Expect: &spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"noted"}}}},
			{Question: "What is my project?"},
		},
		Expect: spec.Expect{
			Answer: &spec.AnswerExpect{Contains: spec.StringList{"zephyr"}},
			Limits: &spec.LimitsExpect{MaxSeconds: 1, MaxFirstTokenSeconds: 0.03},
		},
	}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	cr := sr.Cases[0]
	if cr.Status != StatusPass {
		t.Fatalf("status = %s: %+v", cr.Status, cr.Runs[0])
	}
	rr := cr.Runs[0]
	if len(rr.Turns) != 2 || rr.Turns[1].Answer != "Your project is Zephyr." || rr.Answer != "Your project is Zephyr." {
		t.Errorf("turns = %+v answer=%q", rr.Turns, rr.Answer)
	}
	if rr.LatencyMS != 300 || rr.FirstOutputMS != 20 {
		t.Errorf("latency should sum turns (300ms) and TTFT be turn 1's (20ms): %d %d", rr.LatencyMS, rr.FirstOutputMS)
	}
	if rr.Usage == nil || rr.Usage.TotalTokens != 35 {
		t.Errorf("usage should sum turns: %+v", rr.Usage)
	}
	names := map[string]assert.Status{}
	for _, a := range rr.Assertions {
		names[a.Name] = a.Status
	}
	if names[`turn 1: answer contains "noted"`] != assert.StatusPass {
		t.Errorf("per-turn assertion missing/prefixed wrong: %v", names)
	}
	if names[`answer contains "zephyr"`] != assert.StatusPass || names["limits max_first_token_seconds"] != assert.StatusPass {
		t.Errorf("final assertions: %v", names)
	}
	if len(rr.Turns[0].Assertions) != 1 || rr.Turns[0].Assertions[0].Name != `answer contains "noted"` {
		t.Errorf("turn result should keep unprefixed assertion names: %+v", rr.Turns[0].Assertions)
	}
}

func TestRunMultiTurnPerTurnFailureFailsRun(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(call int, req target.Request) (*target.Result, error) {
		return answerResult(fmt.Sprintf("answer %d", call+1)), nil
	}})
	c := &spec.Case{Name: "mt", Dir: "mt",
		Turns: []spec.Turn{
			{Question: "a", Expect: &spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"nope"}}}},
			{Question: "b"},
		},
		Expect: spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"answer 2"}}},
	}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, _ := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if sr.Cases[0].Status != StatusFail {
		t.Errorf("a failing per-turn assertion must fail the run: %+v", sr.Cases[0].Runs[0])
	}
}

func TestRunMultiTurnErrorMidwayIsRunError(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(call int, req target.Request) (*target.Result, error) {
		if call == 0 {
			return nil, errors.New("connection reset")
		}
		return answerResult("x"), nil
	}})
	c := &spec.Case{Name: "mt", Dir: "mt", Turns: []spec.Turn{{Question: "a"}, {Question: "b"}},
		Expect: spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"x"}}}}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, _ := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if sr.Cases[0].Status != StatusError || !strings.HasPrefix(sr.Cases[0].Runs[0].Error, "turn 1:") {
		t.Errorf("expected turn-labelled run error, got %+v", sr.Cases[0].Runs[0])
	}
}

func TestRunMultiTurnRejectedOnWebhook(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("x"), nil }})
	c := &spec.Case{Name: "mt", Dir: "mt", Turns: []spec.Turn{{Question: "a"}, {Question: "b"}},
		Expect: spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"x"}}}}
	suite := newSuite(spec.SuiteConfig{Agent: "a", WebhookURL: "http://wh/x"}, c)
	sr, _ := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x", TargetOverride: spec.TargetWebhook})
	if sr.Cases[0].Status != StatusError || !strings.Contains(sr.Cases[0].Runs[0].Error, "turns are not supported") {
		t.Errorf("expected webhook/turns error, got %+v", sr.Cases[0].Runs[0])
	}
}

func TestRunMultiTurnJudgeSeesTranscript(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(call int, req target.Request) (*target.Result, error) {
		return answerResult(fmt.Sprintf("A%d", call+1)), nil
	}})
	var gotQ, gotA string
	withJudge(t, func(_ context.Context, cfg judge.Config, q, a, r string) (*judge.Verdict, error) {
		gotQ, gotA = q, a
		if cfg.Model != "judge-model" || cfg.Temperature == nil || *cfg.Temperature != 0 || cfg.RunTag != "tag" {
			t.Errorf("judge config not forwarded: %+v", cfg)
		}
		return &judge.Verdict{Score: 0.8, Reasoning: "coherent"}, nil
	})
	zero := 0.0
	c := &spec.Case{Name: "mt", Dir: "mt", Turns: []spec.Turn{{Question: "Q1"}, {Question: "Q2"}, {Question: "Q3"}},
		Expect: spec.Expect{Judge: &spec.JudgeExpect{Rubric: "r"}}}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, _ := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x", RunTag: "tag",
		Judge: &spec.JudgeAgent{Agent: "j", Model: "judge-model", Temperature: &zero}})
	if sr.Cases[0].Status != StatusPass {
		t.Fatalf("status: %+v", sr.Cases[0].Runs[0])
	}
	if !strings.Contains(gotQ, "[TURN 1 — USER]\nQ1") || !strings.Contains(gotQ, "A2") || !strings.HasSuffix(gotQ, "Q3") || gotA != "A3" {
		t.Errorf("judge question/answer: %q / %q", gotQ, gotA)
	}
	jr := sr.Cases[0].Runs[0].Judge
	if jr == nil || jr.Score != 0.8 || jr.Reasoning != "coherent" || jr.MinScore != judge.DefaultMinScore || jr.Model != "judge-model" {
		t.Errorf("judge record = %+v", jr)
	}
}

func TestRunNegativeCases(t *testing.T) {
	serverErr := &target.ServerError{Status: 401, Message: "Invalid API key", Body: `{"error":"Invalid API key"}`, Where: "v1"}
	withTarget(t, &scriptTarget{fn: func(_ int, req target.Request) (*target.Result, error) {
		switch req.Question {
		case "server-error":
			return nil, serverErr
		case "transport-error":
			return nil, errors.New("dial tcp: connection refused")
		default:
			return answerResult("a fine answer"), nil
		}
	}})

	expectErr := spec.Expect{Error: &spec.ErrorExpect{Status: 401, Contains: spec.StringList{"invalid"}}, Limits: &spec.LimitsExpect{MaxSeconds: 5}}
	pass := &spec.Case{Name: "pass", Dir: "pass", Question: "server-error", Expect: expectErr}
	wrongStatus := &spec.Case{Name: "wrong", Dir: "wrong", Question: "server-error", Expect: spec.Expect{Error: &spec.ErrorExpect{Status: 422}}}
	unexpected := &spec.Case{Name: "success", Dir: "success", Question: "ok", Expect: expectErr}
	transport := &spec.Case{Name: "transport", Dir: "transport", Question: "transport-error", Expect: expectErr}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, pass, wrongStatus, unexpected, transport)

	sr, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*CaseResult{}
	for _, c := range sr.Cases {
		byName[c.Name] = c
	}
	if c := byName["pass"]; c.Status != StatusPass || c.Runs[0].ErrorResponse == nil || c.Runs[0].ErrorResponse.Status != 401 || c.Runs[0].Error != "" {
		t.Errorf("expected error → pass: %+v", c.Runs[0])
	} else {
		names := map[string]assert.Status{}
		for _, a := range c.Runs[0].Assertions {
			names[a.Name] = a.Status
		}
		if names["error status 401"] != assert.StatusPass || names[`error contains "invalid"`] != assert.StatusPass || names["limits max_seconds"] != assert.StatusPass {
			t.Errorf("assertions: %v", names)
		}
	}
	if c := byName["wrong"]; c.Status != StatusFail {
		t.Errorf("wrong status should fail, got %s: %+v", c.Status, c.Runs[0])
	}
	if c := byName["success"]; c.Status != StatusFail || len(c.Runs[0].Assertions) != 1 || !strings.Contains(c.Runs[0].Assertions[0].Message, "expected a server error") {
		t.Errorf("unexpected success should fail: %+v", c.Runs[0])
	}
	if c := byName["transport"]; c.Status != StatusError {
		t.Errorf("transport error stays a run error even with expect.error: %+v", c.Runs[0])
	}
}

func TestRunNegativeCaseOnLastTurnOnly(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(call int, req target.Request) (*target.Result, error) {
		if call == 1 {
			return nil, &target.ServerError{Status: 400, Message: "too long"}
		}
		return answerResult("fine"), nil
	}})
	c := &spec.Case{Name: "mt", Dir: "mt", Turns: []spec.Turn{{Question: "a"}, {Question: "b"}},
		Expect: spec.Expect{Error: &spec.ErrorExpect{Status: 400}}}
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, c)
	sr, _ := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if sr.Cases[0].Status != StatusPass {
		t.Errorf("error on last turn should satisfy expect.error: %+v", sr.Cases[0].Runs[0])
	}
}

func TestRunStampsTTFTFramesAndCost(t *testing.T) {
	withTarget(t, &scriptTarget{fn: func(int, target.Request) (*target.Result, error) {
		return &target.Result{Answer: "ok", Latency: time.Second, FirstOutput: 200 * time.Millisecond,
			Frames: []string{"answer", "end"}, EndFrame: true,
			Usage: &target.Usage{PromptTokens: 1_000_000, CompletionTokens: 100_000, TotalTokens: 1_100_000}}, nil
	}})
	priced := containsCase("priced", "ok")
	priced.Model = "priced-model"
	unpriced := containsCase("unpriced", "ok")
	unpriced.Model = "mystery-model"
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, priced, unpriced)
	sr, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	rr := sr.Cases[0].Runs[0]
	if rr.FirstOutputMS != 200 || len(rr.Frames) != 2 {
		t.Errorf("ttft/frames: %+v", rr)
	}
	if rr.CostUSD == nil || *rr.CostUSD < 1.99 || *rr.CostUSD > 2.01 {
		t.Errorf("cost = %v, want ≈2.00", rr.CostUSD)
	}
	if sr.Cases[1].Runs[0].CostUSD != nil {
		t.Errorf("unknown model must have no cost")
	}
}

func TestRunNoPricingSkipsFetch(t *testing.T) {
	old := fetchPricing
	called := false
	fetchPricing = func(context.Context, *pricing.Table, string) error { called = true; return nil }
	t.Cleanup(func() { fetchPricing = old })
	withTarget(t, &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("ok"), nil }})
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, containsCase("c", "ok"))
	if _, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x", NoPricing: true}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Errorf("NoPricing must skip the fetch")
	}
}

func TestRunSuitePricingWithoutServer(t *testing.T) {
	// Suite-level pricing works even when the server has none (fetch fails).
	old := fetchPricing
	fetchPricing = func(context.Context, *pricing.Table, string) error { return errors.New("401") }
	t.Cleanup(func() { fetchPricing = old })
	withTarget(t, &scriptTarget{fn: func(int, target.Request) (*target.Result, error) {
		return &target.Result{Answer: "ok", Latency: time.Second, Usage: &target.Usage{PromptTokens: 1_000_000, CompletionTokens: 0, TotalTokens: 1_000_000}}, nil
	}})
	c := containsCase("c", "ok")
	c.Model = "m"
	suite := newSuite(spec.SuiteConfig{Agent: "a", Pricing: map[string]spec.ModelPricing{"m": {InputPerMillion: 3}}}, c)
	sr, err := Run(context.Background(), Options{Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	if cost := sr.Cases[0].Runs[0].CostUSD; cost == nil || *cost != 3 {
		t.Errorf("cost = %v, want 3", cost)
	}
}

func TestJudgeQuestion(t *testing.T) {
	if judgeQuestion(nil) != "" {
		t.Errorf("empty history")
	}
	if judgeQuestion([]target.Exchange{{Question: "q"}}) != "q" {
		t.Errorf("single turn should be the bare question")
	}
	got := judgeQuestion([]target.Exchange{{Question: "q1", Answer: "a1"}, {Question: "q2", Answer: "a2"}})
	if !strings.Contains(got, "q1") || !strings.Contains(got, "a1") || !strings.HasSuffix(got, "q2") || strings.Contains(got, "a2") {
		t.Errorf("transcript = %q", got)
	}
}
