package assert

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"docsgpt-cli/internal/bench/spec"
	"docsgpt-cli/internal/bench/target"
)

// Evaluate runs every assertion in exp against res. golden may be nil (the
// golden assertion then fails with a hint to run `bench record`). Absent
// sections produce no Result rows. The judge section is NOT evaluated here —
// the runner calls the judge package and appends its Result.
func Evaluate(exp spec.Expect, res *target.Result, golden *spec.Golden) []Result {
	var out []Result
	if exp.Answer != nil {
		out = append(out, evaluateAnswer(exp.Answer, res.Answer)...)
	}
	if len(exp.JSON) > 0 {
		out = append(out, evaluateJSON(exp.JSON, res.Answer)...)
	}
	if exp.Sources != nil {
		out = append(out, evaluateSources(exp.Sources, res.Sources)...)
	}
	if exp.Tools != nil {
		out = append(out, evaluateTools(exp.Tools, res.ToolCalls)...)
	}
	if exp.Limits != nil {
		out = append(out, evaluateLimits(exp.Limits, res)...)
	}
	if exp.Stream != nil {
		out = append(out, evaluateStream(exp.Stream, res)...)
	}
	if exp.Golden {
		out = append(out, evaluateGolden(golden, res.Answer))
	}
	return out
}

// EvaluateError runs the expect.error section against a server-reported
// error (negative cases). limits.max_seconds is still checked when set, using
// the elapsed time until the error arrived.
func EvaluateError(exp spec.Expect, se *target.ServerError, elapsed time.Duration) []Result {
	var out []Result
	e := exp.Error
	if e == nil {
		return out
	}
	if e.Status > 0 {
		name := fmt.Sprintf("error status %d", e.Status)
		switch {
		case se.Status == e.Status:
			out = append(out, pass(name))
		case se.Status == 0:
			out = append(out, fail(name, fmt.Sprintf("expected HTTP %d but the error carried no HTTP status: %s", e.Status, se.Message)))
		default:
			out = append(out, fail(name, fmt.Sprintf("expected HTTP %d, got %d: %s", e.Status, se.Status, truncate(se.Message))))
		}
	}
	haystack := strings.ToLower(se.Message + "\n" + se.Body)
	for _, sub := range e.Contains {
		name := fmt.Sprintf("error contains %q", sub)
		if strings.Contains(haystack, strings.ToLower(sub)) {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, fmt.Sprintf("error does not contain %q (got: %s)", sub, truncate(firstNonEmpty(se.Message, se.Body)))))
		}
	}
	if len(out) == 0 {
		// Bare `error: {}` — any server error satisfies the case.
		out = append(out, pass("error"))
	}
	if l := exp.Limits; l != nil && l.MaxSeconds > 0 {
		name := "limits max_seconds"
		got := elapsed.Seconds()
		if got <= l.MaxSeconds {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, fmt.Sprintf("expected <= %.3fs, got %.3fs", l.MaxSeconds, got)))
		}
	}
	return out
}

// UnexpectedSuccess is the assertion recorded when expect.error is present but
// the server answered successfully.
func UnexpectedSuccess(answer string) Result {
	return fail("error", "expected a server error, got a successful answer: "+truncate(strings.TrimSpace(answer)))
}

// evaluateStream checks SSE integrity facts recorded by the stream target.
// On targets that do not record frames every check is skipped.
func evaluateStream(st *spec.StreamExpect, res *target.Result) []Result {
	var out []Result
	framesKnown := len(res.Frames) > 0 || res.EndFrame
	if st.EndFrame != nil {
		name := "stream end_frame"
		switch {
		case !framesKnown:
			out = append(out, skip(name, "stream integrity is only recorded by the stream target"))
		case res.EndFrame == *st.EndFrame:
			out = append(out, pass(name))
		case *st.EndFrame:
			out = append(out, fail(name, "stream closed without an end frame"))
		default:
			out = append(out, fail(name, "stream unexpectedly sent an end frame"))
		}
	}
	if st.ErrorFrame != nil {
		name := "stream error_frame"
		switch {
		case !framesKnown:
			out = append(out, skip(name, "stream integrity is only recorded by the stream target"))
		case res.ErrorFrame == *st.ErrorFrame:
			out = append(out, pass(name))
		default:
			out = append(out, fail(name, "stream sent an error frame"))
		}
	}
	switch st.Thought {
	case spec.ThoughtPresent:
		name := "stream thought present"
		if strings.TrimSpace(res.Thought) != "" {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, "no thought/reasoning content was streamed"))
		}
	case spec.ThoughtAbsent:
		name := "stream thought absent"
		if strings.TrimSpace(res.Thought) == "" {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, "unexpected thought/reasoning content: "+truncate(res.Thought)))
		}
	}
	for _, want := range st.FramesContain {
		name := fmt.Sprintf("stream frames_contain %q", want)
		if !framesKnown {
			out = append(out, skip(name, "stream integrity is only recorded by the stream target"))
			continue
		}
		if containsFold(res.Frames, want) {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, fmt.Sprintf("frame %q not observed (seen: %s)", want, strings.Join(res.Frames, ", "))))
		}
	}
	return out
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// evaluateAnswer checks the answer text: exact equals (trimmed both sides),
// case-insensitive contains/not_contains, and regex/not_regex. Each individual
// check is one Result row.
func evaluateAnswer(a *spec.AnswerExpect, answer string) []Result {
	var out []Result

	// equals is a plain string; an unset field is "" — skip it rather than
	// asserting the answer equals the empty string.
	if a.Equals != "" {
		name := fmt.Sprintf("answer equals %q", a.Equals)
		if strings.TrimSpace(answer) == strings.TrimSpace(a.Equals) {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, fmt.Sprintf("expected %q, got %q", a.Equals, truncate(strings.TrimSpace(answer)))))
		}
	}

	lower := strings.ToLower(answer)
	for _, s := range a.Contains {
		name := fmt.Sprintf("answer contains %q", s)
		if strings.Contains(lower, strings.ToLower(s)) {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, fmt.Sprintf("answer does not contain %q", s)))
		}
	}
	for _, s := range a.NotContains {
		name := fmt.Sprintf("answer not_contains %q", s)
		if strings.Contains(lower, strings.ToLower(s)) {
			out = append(out, fail(name, fmt.Sprintf("answer unexpectedly contains %q", s)))
		} else {
			out = append(out, pass(name))
		}
	}
	for _, p := range a.Regex {
		out = append(out, matchRegex("answer regex", p, answer, false))
	}
	for _, p := range a.NotRegex {
		out = append(out, matchRegex("answer not_regex", p, answer, true))
	}
	return out
}

// matchRegex compiles p (the loader validated answer regexes, but compile
// defensively) and matches it against s. negate flips the pass condition.
func matchRegex(label, p, s string, negate bool) Result {
	name := fmt.Sprintf("%s %q", label, p)
	re, err := regexp.Compile(p)
	if err != nil {
		return fail(name, fmt.Sprintf("bad regex %q: %v", p, err))
	}
	matched := re.MatchString(s)
	switch {
	case negate && matched:
		return fail(name, fmt.Sprintf("answer unexpectedly matches /%s/", p))
	case !negate && !matched:
		return fail(name, fmt.Sprintf("answer does not match /%s/", p))
	default:
		return pass(name)
	}
}

func evaluateSources(s *spec.SourcesExpect, sources []map[string]any) []Result {
	var out []Result
	n := len(sources)
	if s.Min != nil {
		name := "sources min"
		if n >= *s.Min {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, fmt.Sprintf("expected at least %d sources, got %d", *s.Min, n)))
		}
	}
	if s.Max != nil {
		name := "sources max"
		if n <= *s.Max {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, fmt.Sprintf("expected at most %d sources, got %d", *s.Max, n)))
		}
	}
	return out
}

func evaluateTools(t *spec.ToolsExpect, calls []target.ToolCallInfo) []Result {
	var out []Result
	for _, want := range t.Called {
		name := fmt.Sprintf("tool called %q", want)
		if toolCalled(calls, want) {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, fmt.Sprintf("tool %q was not called (called: %s)", want, toolNames(calls))))
		}
	}
	for _, want := range t.NotCalled {
		name := fmt.Sprintf("tool not_called %q", want)
		if toolCalled(calls, want) {
			out = append(out, fail(name, fmt.Sprintf("tool %q was called but should not have been", want)))
		} else {
			out = append(out, pass(name))
		}
	}
	return out
}

func toolCalled(calls []target.ToolCallInfo, name string) bool {
	for _, c := range calls {
		if strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

func toolNames(calls []target.ToolCallInfo) string {
	if len(calls) == 0 {
		return "none"
	}
	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.Name
	}
	return strings.Join(names, ", ")
}

func evaluateLimits(l *spec.LimitsExpect, res *target.Result) []Result {
	var out []Result
	if l.MaxSeconds > 0 {
		name := "limits max_seconds"
		got := res.Latency.Seconds()
		if got <= l.MaxSeconds {
			out = append(out, pass(name))
		} else {
			out = append(out, fail(name, fmt.Sprintf("expected <= %.3fs, got %.3fs", l.MaxSeconds, got)))
		}
	}
	if l.MaxFirstTokenSeconds > 0 {
		name := "limits max_first_token_seconds"
		switch {
		case res.FirstOutput <= 0:
			out = append(out, skip(name, "time to first token not observed by this target (stream, or v1 with stream: true)"))
		case res.FirstOutput.Seconds() <= l.MaxFirstTokenSeconds:
			out = append(out, pass(name))
		default:
			out = append(out, fail(name, fmt.Sprintf("expected first output <= %.3fs, got %.3fs", l.MaxFirstTokenSeconds, res.FirstOutput.Seconds())))
		}
	}
	if l.MaxTotalTokens > 0 {
		name := "limits max_total_tokens"
		switch {
		case res.Usage == nil:
			out = append(out, skip(name, "usage not reported by this target (v1 only)"))
		case res.Usage.TotalTokens <= l.MaxTotalTokens:
			out = append(out, pass(name))
		default:
			out = append(out, fail(name, fmt.Sprintf("expected <= %d tokens, got %d", l.MaxTotalTokens, res.Usage.TotalTokens)))
		}
	}
	return out
}

// evaluateGolden compares the answer against the recorded golden answer after
// collapsing whitespace. A missing golden fails with a hint to record one.
func evaluateGolden(golden *spec.Golden, answer string) Result {
	const name = "golden"
	if golden == nil {
		return fail(name, "no golden.json — run `docsgpt-cli bench record`")
	}
	if normalizeWhitespace(answer) == normalizeWhitespace(golden.Answer) {
		return pass(name)
	}
	return fail(name, fmt.Sprintf("answer differs from golden.json\n  golden: %s\n  got:    %s",
		truncate(normalizeWhitespace(golden.Answer)), truncate(normalizeWhitespace(answer))))
}

var whitespaceRun = regexp.MustCompile(`\s+`)

// normalizeWhitespace collapses every run of whitespace to a single space and
// trims the ends, so golden comparisons ignore reflowing and indentation.
func normalizeWhitespace(s string) string {
	return strings.TrimSpace(whitespaceRun.ReplaceAllString(s, " "))
}

// truncate shortens long rendered values for assertion messages.
func truncate(s string) string {
	const max = 200
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
