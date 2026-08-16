package assert

import (
	"strings"
	"testing"
	"time"

	"docsgpt-cli/internal/bench/spec"
	"docsgpt-cli/internal/bench/target"
)

func boolPtr(b bool) *bool { return &b }

func TestEvaluateError(t *testing.T) {
	se := &target.ServerError{Status: 401, Message: "Invalid API key", Body: `{"error":"Invalid API key"}`}

	// status + contains pass
	rs := EvaluateError(spec.Expect{Error: &spec.ErrorExpect{Status: 401, Contains: spec.StringList{"invalid api"}}}, se, time.Second)
	if len(rs) != 2 || rs[0].Status != StatusPass || rs[1].Status != StatusPass {
		t.Errorf("pass case: %+v", rs)
	}
	// wrong status
	rs = EvaluateError(spec.Expect{Error: &spec.ErrorExpect{Status: 422}}, se, time.Second)
	if len(rs) != 1 || rs[0].Status != StatusFail || !strings.Contains(rs[0].Message, "got 401") {
		t.Errorf("wrong status: %+v", rs)
	}
	// missing substring
	rs = EvaluateError(spec.Expect{Error: &spec.ErrorExpect{Contains: spec.StringList{"not found"}}}, se, time.Second)
	if len(rs) != 1 || rs[0].Status != StatusFail {
		t.Errorf("missing substring: %+v", rs)
	}
	// bare error: {} — any server error passes
	rs = EvaluateError(spec.Expect{Error: &spec.ErrorExpect{}}, se, time.Second)
	if len(rs) != 1 || rs[0].Status != StatusPass || rs[0].Name != "error" {
		t.Errorf("bare error: %+v", rs)
	}
	// status expected but error carried none (webhook task failure)
	rs = EvaluateError(spec.Expect{Error: &spec.ErrorExpect{Status: 500}}, &target.ServerError{Message: "boom"}, time.Second)
	if len(rs) != 1 || rs[0].Status != StatusFail || !strings.Contains(rs[0].Message, "no HTTP status") {
		t.Errorf("no status: %+v", rs)
	}
	// limits.max_seconds still applies to error responses
	rs = EvaluateError(spec.Expect{Error: &spec.ErrorExpect{}, Limits: &spec.LimitsExpect{MaxSeconds: 0.5}}, se, 2*time.Second)
	if len(rs) != 2 || rs[1].Name != "limits max_seconds" || rs[1].Status != StatusFail {
		t.Errorf("limits with error: %+v", rs)
	}
	// nil section is a no-op
	if rs := EvaluateError(spec.Expect{}, se, 0); len(rs) != 0 {
		t.Errorf("nil section: %+v", rs)
	}
}

func TestUnexpectedSuccess(t *testing.T) {
	r := UnexpectedSuccess("hello there")
	if r.Status != StatusFail || r.Name != "error" || !strings.Contains(r.Message, "hello there") {
		t.Errorf("%+v", r)
	}
}

func TestEvaluateStream(t *testing.T) {
	res := &target.Result{Answer: "x", Thought: "hmm", Frames: []string{"message_id", "thought", "answer", "source", "id", "end"}, EndFrame: true}
	exp := spec.Expect{Stream: &spec.StreamExpect{
		EndFrame: boolPtr(true), ErrorFrame: boolPtr(false), Thought: spec.ThoughtPresent, FramesContain: spec.StringList{"source", "message_id"},
	}}
	st := statuses(Evaluate(exp, res, nil))
	for _, name := range []string{"stream end_frame", "stream error_frame", "stream thought present", `stream frames_contain "source"`, `stream frames_contain "message_id"`} {
		if st[name] != StatusPass {
			t.Errorf("%s = %v (all: %v)", name, st[name], st)
		}
	}

	// Failures: no end frame, thought absent wanted, missing frame.
	res2 := &target.Result{Answer: "x", Thought: "hmm", Frames: []string{"answer"}}
	exp2 := spec.Expect{Stream: &spec.StreamExpect{EndFrame: boolPtr(true), Thought: spec.ThoughtAbsent, FramesContain: spec.StringList{"source"}}}
	st = statuses(Evaluate(exp2, res2, nil))
	if st["stream end_frame"] != StatusFail || st["stream thought absent"] != StatusFail || st[`stream frames_contain "source"`] != StatusFail {
		t.Errorf("failures: %v", st)
	}

	// Frames unknown (non-stream target): frame checks skip, thought still evaluated.
	res3 := &target.Result{Answer: "x"}
	exp3 := spec.Expect{Stream: &spec.StreamExpect{EndFrame: boolPtr(true), Thought: spec.ThoughtAbsent, FramesContain: spec.StringList{"end"}}}
	st = statuses(Evaluate(exp3, res3, nil))
	if st["stream end_frame"] != StatusSkip || st[`stream frames_contain "end"`] != StatusSkip || st["stream thought absent"] != StatusPass {
		t.Errorf("unknown frames: %v", st)
	}
}

func TestEvaluateLimitsFirstToken(t *testing.T) {
	exp := spec.Expect{Limits: &spec.LimitsExpect{MaxFirstTokenSeconds: 1}}
	st := statuses(Evaluate(exp, &target.Result{Latency: 5 * time.Second, FirstOutput: 500 * time.Millisecond}, nil))
	if st["limits max_first_token_seconds"] != StatusPass {
		t.Errorf("fast TTFT should pass: %v", st)
	}
	st = statuses(Evaluate(exp, &target.Result{Latency: 5 * time.Second, FirstOutput: 3 * time.Second}, nil))
	if st["limits max_first_token_seconds"] != StatusFail {
		t.Errorf("slow TTFT should fail: %v", st)
	}
	st = statuses(Evaluate(exp, &target.Result{Latency: 5 * time.Second}, nil))
	if st["limits max_first_token_seconds"] != StatusSkip {
		t.Errorf("unobserved TTFT should skip: %v", st)
	}
}
