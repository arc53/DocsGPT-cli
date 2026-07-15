package assert

import (
	"fmt"
	"regexp"
	"strings"

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
	if exp.Golden {
		out = append(out, evaluateGolden(golden, res.Answer))
	}
	return out
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
