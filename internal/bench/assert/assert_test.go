package assert

import (
	"strings"
	"testing"
	"time"

	"docsgpt-cli/internal/bench/spec"
	"docsgpt-cli/internal/bench/target"
	"gopkg.in/yaml.v3"
)

func intPtr(i int) *int { return &i }

// only asserts exactly one result and returns it.
func only(t *testing.T, rs []Result) Result {
	t.Helper()
	if len(rs) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(rs), rs)
	}
	return rs[0]
}

func statuses(rs []Result) map[string]Status {
	m := make(map[string]Status, len(rs))
	for _, r := range rs {
		m[r.Name] = r.Status
	}
	return m
}

// evalJSON evaluates a single json-path matcher against answer.
func evalJSON(answer, path string, matcher any) []Result {
	return Evaluate(spec.Expect{JSON: map[string]any{path: matcher}}, &target.Result{Answer: answer}, nil)
}

func TestEvaluateAnswer(t *testing.T) {
	tests := []struct {
		name   string
		expect spec.AnswerExpect
		answer string
		want   map[string]Status
	}{
		{
			name:   "equals trims both sides",
			expect: spec.AnswerExpect{Equals: "  hello world  "},
			answer: "hello world",
			want:   map[string]Status{`answer equals "  hello world  "`: StatusPass},
		},
		{
			name:   "equals mismatch",
			expect: spec.AnswerExpect{Equals: "hello"},
			answer: "goodbye",
			want:   map[string]Status{`answer equals "hello"`: StatusFail},
		},
		{
			name:   "contains case-insensitive pass",
			expect: spec.AnswerExpect{Contains: spec.StringList{"45 Minutes"}},
			answer: "It takes 45 minutes to arrive.",
			want:   map[string]Status{`answer contains "45 Minutes"`: StatusPass},
		},
		{
			name:   "contains fail",
			expect: spec.AnswerExpect{Contains: spec.StringList{"nope"}},
			answer: "something else",
			want:   map[string]Status{`answer contains "nope"`: StatusFail},
		},
		{
			name:   "not_contains pass and fail",
			expect: spec.AnswerExpect{NotContains: spec.StringList{"secret", "public"}},
			answer: "this is public info",
			want: map[string]Status{
				`answer not_contains "secret"`: StatusPass,
				`answer not_contains "public"`: StatusFail,
			},
		},
		{
			name:   "regex pass",
			expect: spec.AnswerExpect{Regex: spec.StringList{`\d+ minutes`}},
			answer: "about 45 minutes",
			want:   map[string]Status{`answer regex "\\d+ minutes"`: StatusPass},
		},
		{
			name:   "not_regex fail when it matches",
			expect: spec.AnswerExpect{NotRegex: spec.StringList{`\d+`}},
			answer: "there are 3 apples",
			want:   map[string]Status{`answer not_regex "\\d+"`: StatusFail},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := tt.expect
			got := statuses(Evaluate(spec.Expect{Answer: &exp}, &target.Result{Answer: tt.answer}, nil))
			for name, want := range tt.want {
				if got[name] != want {
					t.Errorf("%q: got %q, want %q (all: %v)", name, got[name], want, got)
				}
			}
		})
	}
}

func TestEvaluateAnswerEqualsEmptySkipped(t *testing.T) {
	// An unset equals ("") must not produce a row.
	rs := Evaluate(spec.Expect{Answer: &spec.AnswerExpect{Contains: spec.StringList{"x"}}},
		&target.Result{Answer: "x"}, nil)
	for _, r := range rs {
		if strings.HasPrefix(r.Name, "answer equals") {
			t.Fatalf("unexpected equals row: %+v", r)
		}
	}
}

func TestParseAnswerJSON(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		wantOK bool
		want   string
	}{
		{"clean object", `{"a":1}`, true, `{"a":1}`},
		{"clean with whitespace", "  {\"a\":1}\n", true, `{"a":1}`},
		{"fenced json", "```json\n{\"a\":1}\n```", true, `{"a":1}`},
		{"fenced bare", "```\n{\"a\":1}\n```", true, `{"a":1}`},
		{"prose wrapped", `Here you go: {"a":1} cheers`, true, `{"a":1}`},
		{"array top-level", `[1,2,3]`, true, `[1,2,3]`},
		{"garbage", `not json at all`, false, ""},
		{"fence with trailing prose", "```json\n{\"a\":1}\n```\nnote", true, `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseAnswerJSON(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tt.wantOK, got)
			}
			if tt.wantOK && got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEvaluateJSONScalarEquality(t *testing.T) {
	tests := []struct {
		name    string
		answer  string
		path    string
		matcher any
		want    Status
	}{
		{"string equal", `{"status":"ok"}`, "status", "ok", StatusPass},
		{"string mismatch", `{"status":"ok"}`, "status", "bad", StatusFail},
		{"int equal", `{"n":42}`, "n", 42, StatusPass},
		{"float tolerance", `{"n":1.0000001}`, "n", 1.0, StatusPass},
		{"float outside tolerance", `{"n":1.5}`, "n", 1.0, StatusFail},
		{"bool equal", `{"ok":true}`, "ok", true, StatusPass},
		{"string vs number mismatch", `{"n":45}`, "n", "45", StatusFail},
		{"number vs string mismatch", `{"s":"45"}`, "s", 45, StatusFail},
		{"missing path", `{"a":1}`, "b", "x", StatusFail},
		{"nested path", `{"a":{"b":"c"}}`, "a.b", "c", StatusPass},
		{"array element", `{"xs":[1,2,3]}`, "xs.1", 2, StatusPass},
		{"bare list equals array", `{"xs":[1,2,3]}`, "xs", []any{1, 2, 3}, StatusPass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := only(t, evalJSON(tt.answer, tt.path, tt.matcher))
			if r.Status != tt.want {
				t.Errorf("got %q (%s), want %q", r.Status, r.Message, tt.want)
			}
		})
	}
}

func TestEvaluateJSONMatchers(t *testing.T) {
	tests := []struct {
		name    string
		answer  string
		path    string
		matcher map[string]any
		want    Status
	}{
		{"equals deep object", `{"o":{"b":2,"a":1}}`, "o", map[string]any{"equals": map[string]any{"a": 1, "b": 2}}, StatusPass},
		{"equals array", `{"xs":[1,2]}`, "xs", map[string]any{"equals": []any{1, 2}}, StatusPass},
		{"contains string", `{"s":"Hello World"}`, "s", map[string]any{"contains": "world"}, StatusPass},
		{"contains all list", `{"s":"alpha beta"}`, "s", map[string]any{"contains": []any{"alpha", "beta"}}, StatusPass},
		{"contains missing one", `{"s":"alpha"}`, "s", map[string]any{"contains": []any{"alpha", "beta"}}, StatusFail},
		{"contains array element", `{"xs":["apple","banana"]}`, "xs", map[string]any{"contains": "nan"}, StatusPass},
		{"not_contains pass", `{"s":"clean"}`, "s", map[string]any{"not_contains": "dirty"}, StatusPass},
		{"not_contains fail", `{"s":"dirty"}`, "s", map[string]any{"not_contains": "dirty"}, StatusFail},
		{"regex pass", `{"s":"v1.2.3"}`, "s", map[string]any{"regex": `v\d+\.\d+\.\d+`}, StatusPass},
		{"regex fail", `{"s":"nope"}`, "s", map[string]any{"regex": `\d+`}, StatusFail},
		{"regex bad pattern", `{"s":"x"}`, "s", map[string]any{"regex": `(`}, StatusFail},
		{"not_none present", `{"a":1}`, "a", map[string]any{"not_none": true}, StatusPass},
		{"not_none missing", `{"a":1}`, "b", map[string]any{"not_none": true}, StatusFail},
		{"not_none null", `{"a":null}`, "a", map[string]any{"not_none": true}, StatusFail},
		{"is_none missing", `{"a":1}`, "b", map[string]any{"is_none": true}, StatusPass},
		{"is_none null", `{"a":null}`, "a", map[string]any{"is_none": true}, StatusPass},
		{"is_none present fail", `{"a":1}`, "a", map[string]any{"is_none": true}, StatusFail},
		{"starts_with pass", `{"s":"prefix-body"}`, "s", map[string]any{"starts_with": "prefix"}, StatusPass},
		{"starts_with fail", `{"s":"body"}`, "s", map[string]any{"starts_with": "prefix"}, StatusFail},
		{"not_starts_with pass", `{"s":"body"}`, "s", map[string]any{"not_starts_with": "prefix"}, StatusPass},
		{"one_of pass string", `{"s":"b"}`, "s", map[string]any{"one_of": []any{"a", "b", "c"}}, StatusPass},
		{"one_of fail", `{"s":"z"}`, "s", map[string]any{"one_of": []any{"a", "b"}}, StatusFail},
		{"one_of number", `{"n":2}`, "n", map[string]any{"one_of": []any{1, 2, 3}}, StatusPass},
		{"gt pass", `{"n":5}`, "n", map[string]any{"gt": 3}, StatusPass},
		{"gt fail", `{"n":2}`, "n", map[string]any{"gt": 3}, StatusFail},
		{"gte boundary", `{"n":3}`, "n", map[string]any{"gte": 3}, StatusPass},
		{"lt pass", `{"n":2}`, "n", map[string]any{"lt": 3}, StatusPass},
		{"lte boundary", `{"n":3}`, "n", map[string]any{"lte": 3}, StatusPass},
		{"gt non-number", `{"s":"x"}`, "s", map[string]any{"gt": 3}, StatusFail},
		{"length array", `{"xs":[1,2,3]}`, "xs", map[string]any{"length": 3}, StatusPass},
		{"length string", `{"s":"abc"}`, "s", map[string]any{"length": 3}, StatusPass},
		{"length object", `{"o":{"a":1,"b":2}}`, "o", map[string]any{"length": 2}, StatusPass},
		{"min_length pass", `{"xs":[1,2,3]}`, "xs", map[string]any{"min_length": 2}, StatusPass},
		{"max_length fail", `{"xs":[1,2,3]}`, "xs", map[string]any{"max_length": 2}, StatusFail},
		{"length non-lengthable", `{"n":5}`, "n", map[string]any{"length": 1}, StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := only(t, evalJSON(tt.answer, tt.path, tt.matcher))
			if r.Status != tt.want {
				t.Errorf("got %q (%s), want %q", r.Status, r.Message, tt.want)
			}
		})
	}
}

func TestEvaluateJSONCombinedMatchers(t *testing.T) {
	// Multiple matcher keys on one path -> one Result each, deterministic order.
	rs := evalJSON(`{"n":5}`, "n", map[string]any{"gt": 1, "lt": 10, "not_none": true})
	if len(rs) != 3 {
		t.Fatalf("expected 3 results, got %d: %+v", len(rs), rs)
	}
	wantNames := []string{"json n not_none", "json n gt", "json n lt"}
	for i, r := range rs {
		if r.Name != wantNames[i] {
			t.Errorf("result %d name = %q, want %q", i, r.Name, wantNames[i])
		}
		if r.Status != StatusPass {
			t.Errorf("%q: %s (%s)", r.Name, r.Status, r.Message)
		}
	}
}

func TestEvaluateJSONUnknownMatcher(t *testing.T) {
	r := only(t, evalJSON(`{"a":1}`, "a", map[string]any{"containz": "x"}))
	if r.Status != StatusFail || !strings.Contains(r.Message, "unknown matcher") {
		t.Fatalf("expected unknown-matcher fail, got %+v", r)
	}
}

func TestEvaluateJSONInvalidAnswer(t *testing.T) {
	exp := spec.Expect{JSON: map[string]any{
		"a": "x",
		"b": map[string]any{"gt": 1},
	}}
	rs := Evaluate(exp, &target.Result{Answer: "this is not json"}, nil)
	if len(rs) != 2 {
		t.Fatalf("expected 2 results (one per path), got %d: %+v", len(rs), rs)
	}
	for _, r := range rs {
		if r.Status != StatusFail || r.Message != "answer is not valid JSON" {
			t.Errorf("%q: got %q/%q", r.Name, r.Status, r.Message)
		}
	}
}

func TestEvaluateJSONScalarNameHasNoOp(t *testing.T) {
	r := only(t, evalJSON(`{"status":"ok"}`, "status", "ok"))
	if r.Name != "json status" {
		t.Errorf("scalar form name = %q, want %q", r.Name, "json status")
	}
}

// TestEvaluateJSONViaYAML confirms matcher maps decoded from YAML (the real
// path) type-assert as map[string]any and evaluate correctly.
func TestEvaluateJSONViaYAML(t *testing.T) {
	var exp spec.Expect
	yamlDoc := "" +
		"json:\n" +
		"  status: ok\n" +
		"  count:\n" +
		"    gte: 2\n" +
		"  items:\n" +
		"    min_length: 1\n"
	if err := yaml.Unmarshal([]byte(yamlDoc), &exp); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	answer := `{"status":"ok","count":3,"items":["a","b"]}`
	rs := Evaluate(exp, &target.Result{Answer: answer}, nil)
	for _, r := range rs {
		if r.Status != StatusPass {
			t.Errorf("%q: %s (%s)", r.Name, r.Status, r.Message)
		}
	}
	if len(rs) != 3 {
		t.Fatalf("expected 3 results, got %d: %+v", len(rs), rs)
	}
}

func TestEvaluateSources(t *testing.T) {
	src := func(n int) []map[string]any {
		out := make([]map[string]any, n)
		for i := range out {
			out[i] = map[string]any{"i": i}
		}
		return out
	}
	tests := []struct {
		name    string
		sources []map[string]any
		expect  spec.SourcesExpect
		want    map[string]Status
	}{
		{"min pass", src(3), spec.SourcesExpect{Min: intPtr(2)}, map[string]Status{"sources min": StatusPass}},
		{"min fail", src(1), spec.SourcesExpect{Min: intPtr(2)}, map[string]Status{"sources min": StatusFail}},
		{"max pass", src(1), spec.SourcesExpect{Max: intPtr(2)}, map[string]Status{"sources max": StatusPass}},
		{"max fail", src(5), spec.SourcesExpect{Max: intPtr(2)}, map[string]Status{"sources max": StatusFail}},
		{"both", src(2), spec.SourcesExpect{Min: intPtr(1), Max: intPtr(3)}, map[string]Status{"sources min": StatusPass, "sources max": StatusPass}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := tt.expect
			got := statuses(Evaluate(spec.Expect{Sources: &exp}, &target.Result{Sources: tt.sources}, nil))
			for name, want := range tt.want {
				if got[name] != want {
					t.Errorf("%q: got %q, want %q", name, got[name], want)
				}
			}
		})
	}
}

func TestEvaluateTools(t *testing.T) {
	calls := []target.ToolCallInfo{{Name: "Web_Search"}, {Name: "calculator"}}
	tests := []struct {
		name   string
		expect spec.ToolsExpect
		want   map[string]Status
	}{
		{"called case-insensitive", spec.ToolsExpect{Called: spec.StringList{"web_search"}}, map[string]Status{`tool called "web_search"`: StatusPass}},
		{"called missing", spec.ToolsExpect{Called: spec.StringList{"grep"}}, map[string]Status{`tool called "grep"`: StatusFail}},
		{"not_called pass", spec.ToolsExpect{NotCalled: spec.StringList{"grep"}}, map[string]Status{`tool not_called "grep"`: StatusPass}},
		{"not_called fail", spec.ToolsExpect{NotCalled: spec.StringList{"calculator"}}, map[string]Status{`tool not_called "calculator"`: StatusFail}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := tt.expect
			got := statuses(Evaluate(spec.Expect{Tools: &exp}, &target.Result{ToolCalls: calls}, nil))
			for name, want := range tt.want {
				if got[name] != want {
					t.Errorf("%q: got %q, want %q", name, got[name], want)
				}
			}
		})
	}
}

func TestEvaluateLimits(t *testing.T) {
	t.Run("max_seconds pass", func(t *testing.T) {
		r := only(t, Evaluate(spec.Expect{Limits: &spec.LimitsExpect{MaxSeconds: 5}},
			&target.Result{Latency: 3 * time.Second}, nil))
		if r.Status != StatusPass {
			t.Errorf("got %q (%s)", r.Status, r.Message)
		}
	})
	t.Run("max_seconds fail", func(t *testing.T) {
		r := only(t, Evaluate(spec.Expect{Limits: &spec.LimitsExpect{MaxSeconds: 1}},
			&target.Result{Latency: 3 * time.Second}, nil))
		if r.Status != StatusFail {
			t.Errorf("got %q", r.Status)
		}
	})
	t.Run("tokens skip when usage nil", func(t *testing.T) {
		r := only(t, Evaluate(spec.Expect{Limits: &spec.LimitsExpect{MaxTotalTokens: 100}},
			&target.Result{Usage: nil}, nil))
		if r.Status != StatusSkip {
			t.Fatalf("got %q, want skip", r.Status)
		}
		if !strings.Contains(r.Message, "usage not reported") {
			t.Errorf("skip message = %q", r.Message)
		}
	})
	t.Run("tokens pass", func(t *testing.T) {
		r := only(t, Evaluate(spec.Expect{Limits: &spec.LimitsExpect{MaxTotalTokens: 100}},
			&target.Result{Usage: &target.Usage{TotalTokens: 80}}, nil))
		if r.Status != StatusPass {
			t.Errorf("got %q (%s)", r.Status, r.Message)
		}
	})
	t.Run("tokens fail", func(t *testing.T) {
		r := only(t, Evaluate(spec.Expect{Limits: &spec.LimitsExpect{MaxTotalTokens: 100}},
			&target.Result{Usage: &target.Usage{TotalTokens: 250}}, nil))
		if r.Status != StatusFail {
			t.Errorf("got %q", r.Status)
		}
	})
}

func TestEvaluateGolden(t *testing.T) {
	t.Run("missing golden", func(t *testing.T) {
		r := only(t, Evaluate(spec.Expect{Golden: true}, &target.Result{Answer: "x"}, nil))
		if r.Status != StatusFail || !strings.Contains(r.Message, "bench record") {
			t.Fatalf("got %+v", r)
		}
	})
	t.Run("match after whitespace normalize", func(t *testing.T) {
		g := &spec.Golden{Answer: "the   quick\nbrown fox"}
		r := only(t, Evaluate(spec.Expect{Golden: true}, &target.Result{Answer: "the quick brown fox\n"}, g))
		if r.Status != StatusPass {
			t.Errorf("got %q (%s)", r.Status, r.Message)
		}
	})
	t.Run("mismatch", func(t *testing.T) {
		g := &spec.Golden{Answer: "expected answer"}
		r := only(t, Evaluate(spec.Expect{Golden: true}, &target.Result{Answer: "different answer"}, g))
		if r.Status != StatusFail {
			t.Errorf("got %q", r.Status)
		}
	})
}

func TestEvaluateJudgeProducesNothing(t *testing.T) {
	rs := Evaluate(spec.Expect{Judge: &spec.JudgeExpect{Rubric: "grade it"}},
		&target.Result{Answer: "x"}, nil)
	if len(rs) != 0 {
		t.Fatalf("judge section must not produce Results, got %+v", rs)
	}
}

func TestJSONMatchersFailOnMissingPath(t *testing.T) {
	res := &target.Result{Answer: `{"a": 1}`}
	for name, matcher := range map[string]any{
		"regex":       map[string]any{"regex": ".*"},
		"starts_with": map[string]any{"starts_with": ""},
	} {
		out := Evaluate(spec.Expect{JSON: map[string]any{"missing": matcher}}, res, nil)
		if len(out) != 1 || out[0].Status != StatusFail {
			t.Errorf("%s on missing path: want fail, got %+v", name, out)
		}
	}
}

func TestParseAnswerJSONArrayInProse(t *testing.T) {
	got, ok := ParseAnswerJSON("Here you go: [1, 2, 3] enjoy!")
	if !ok || got != "[1, 2, 3]" {
		t.Errorf("array-in-prose: got %q ok=%v", got, ok)
	}
}

func TestPresenceAcceptsYAMLishBoolStrings(t *testing.T) {
	res := &target.Result{Answer: `{"a": 1}`}
	// yaml 1.2 parses `not_none: yes` as the string "yes" — must mean true.
	out := Evaluate(spec.Expect{JSON: map[string]any{"a": map[string]any{"not_none": "yes"}}}, res, nil)
	if len(out) != 1 || out[0].Status != StatusPass {
		t.Errorf("not_none 'yes': want pass, got %+v", out)
	}
}
