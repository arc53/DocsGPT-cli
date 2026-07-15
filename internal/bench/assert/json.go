package assert

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// numTolerance is the absolute tolerance for numeric equality comparisons.
const numTolerance = 1e-6

// ParseAnswerJSON extracts a JSON document from an answer that may be wrapped
// in a markdown code fence or surrounding prose. It returns the extracted JSON
// and whether it parses. The rules, applied in order:
//  1. trim surrounding whitespace;
//  2. if the text is a ```json … ``` (or bare ``` … ```) fence, strip it;
//  3. if the result is valid JSON, use it;
//  4. otherwise take the substring from the first '{' to the last '}' and try
//     that;
//  5. if nothing parses, return the processed text with ok=false.
//
// It is exported so `bench record`/report can reuse the same preprocessor.
func ParseAnswerJSON(answer string) (string, bool) {
	s := strings.TrimSpace(answer)
	if strings.HasPrefix(s, "```") {
		s = stripCodeFence(s)
	}
	if json.Valid([]byte(s)) {
		return s, true
	}
	if start := strings.Index(s, "{"); start >= 0 {
		if end := strings.LastIndex(s, "}"); end > start {
			cand := s[start : end+1]
			if json.Valid([]byte(cand)) {
				return cand, true
			}
		}
	}
	// Same fallback for a top-level array wrapped in prose.
	if start := strings.Index(s, "["); start >= 0 {
		if end := strings.LastIndex(s, "]"); end > start {
			cand := s[start : end+1]
			if json.Valid([]byte(cand)) {
				return cand, true
			}
		}
	}
	return s, false
}

// stripCodeFence removes a leading ```lang line and the trailing ``` from a
// fenced block. Content after the closing fence (if any) is left in place for
// the brace-extraction fallback to handle.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:] // drop the opening ```lang line
	} else {
		s = strings.TrimPrefix(s, "```")
	}
	s = strings.TrimRight(s, " \t\r\n")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// evaluateJSON parses the answer as JSON once, then runs every path matcher.
// Paths are sorted for deterministic output. If the answer is not JSON, every
// path fails with the same reason.
func evaluateJSON(m map[string]any, answer string) []Result {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	jsonStr, ok := ParseAnswerJSON(answer)
	if !ok {
		out := make([]Result, 0, len(paths))
		for _, p := range paths {
			out = append(out, fail("json "+p, "answer is not valid JSON"))
		}
		return out
	}

	var out []Result
	for _, p := range paths {
		out = append(out, evaluatePath(p, m[p], gjson.Get(jsonStr, p))...)
	}
	return out
}

// evaluatePath dispatches one path's matcher. A scalar (or bare list) is an
// equality check; a map holds one or more matcher keys, each its own check.
func evaluatePath(path string, matcher any, got gjson.Result) []Result {
	mm, ok := matcher.(map[string]any)
	if !ok {
		return []Result{checkEquals("json "+path, matcher, got)}
	}

	var out []Result
	for _, op := range matcherOps {
		if v, present := mm[op]; present {
			out = append(out, runMatcher(path, op, v, got))
		}
	}
	// Fail loudly on typo'd matcher keys, mirroring the spec loader's strict
	// unknown-field handling (json matchers are untyped, so validate here).
	var unknown []string
	for k := range mm {
		if !knownOps[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		out = append(out, fail(fmt.Sprintf("json %s %s", path, k), fmt.Sprintf("unknown matcher %q", k)))
	}
	return out
}

// matcherOps is the fixed evaluation order for combined matcher keys, keeping
// Evaluate's output deterministic.
var matcherOps = []string{
	"equals", "not_none", "is_none",
	"contains", "not_contains", "regex",
	"starts_with", "not_starts_with", "one_of",
	"gt", "gte", "lt", "lte",
	"length", "min_length", "max_length",
}

var knownOps = func() map[string]bool {
	m := make(map[string]bool, len(matcherOps))
	for _, op := range matcherOps {
		m[op] = true
	}
	return m
}()

func runMatcher(path, op string, v any, got gjson.Result) Result {
	name := fmt.Sprintf("json %s %s", path, op)
	switch op {
	case "equals":
		return checkEquals(name, v, got)
	case "not_none":
		return checkPresence(name, v, got, false)
	case "is_none":
		return checkPresence(name, v, got, true)
	case "contains":
		return checkContains(name, v, got, false)
	case "not_contains":
		return checkContains(name, v, got, true)
	case "regex":
		return checkRegex(name, v, got)
	case "starts_with":
		return checkStartsWith(name, v, got, false)
	case "not_starts_with":
		return checkStartsWith(name, v, got, true)
	case "one_of":
		return checkOneOf(name, v, got)
	case "gt", "gte", "lt", "lte":
		return checkCompare(name, op, v, got)
	case "length", "min_length", "max_length":
		return checkLength(name, op, v, got)
	}
	return fail(name, fmt.Sprintf("unknown matcher %q", op))
}

// checkEquals asserts the value at got equals expected. Numbers compare with
// tolerance; string-vs-number (and other type) mismatches fail with a clear
// message; arrays/objects compare via normalized JSON.
func checkEquals(name string, expected any, got gjson.Result) Result {
	if !got.Exists() {
		return fail(name, fmt.Sprintf("expected %s, but path is missing", render(expected)))
	}
	if _, ok := asNumber(expected); ok && got.Type != gjson.Number {
		return fail(name, fmt.Sprintf("expected number %s, but answer value is %s (%s)", render(expected), jsonKind(got), renderGot(got)))
	}
	if _, ok := expected.(string); ok && got.Type != gjson.String {
		return fail(name, fmt.Sprintf("expected string %s, but answer value is %s (%s)", render(expected), jsonKind(got), renderGot(got)))
	}
	if valuesEqual(expected, got) {
		return pass(name)
	}
	return fail(name, fmt.Sprintf("expected %s, got %s", render(expected), renderGot(got)))
}

// valuesEqual reports whether expected equals the value at got, applying
// numeric tolerance and comparing arrays/objects via normalized JSON.
func valuesEqual(expected any, got gjson.Result) bool {
	if !got.Exists() {
		return false
	}
	if en, ok := asNumber(expected); ok {
		return got.Type == gjson.Number && math.Abs(en-got.Num) <= numTolerance
	}
	if es, ok := expected.(string); ok {
		return got.Type == gjson.String && es == got.Str
	}
	if eb, ok := expected.(bool); ok {
		return (got.Type == gjson.True || got.Type == gjson.False) && eb == got.Bool()
	}
	// Arrays, objects, and null: compare canonical JSON (json.Marshal sorts
	// object keys, so equality is order-independent for object keys).
	eb, err1 := json.Marshal(expected)
	gb, err2 := json.Marshal(got.Value())
	return err1 == nil && err2 == nil && string(eb) == string(gb)
}

func checkPresence(name string, v any, got gjson.Result, isNoneKey bool) Result {
	present := got.Exists() && got.Type != gjson.Null
	// not_none:true wants present; is_none:true wants absent; a false flag
	// inverts the expectation.
	wantPresent := asBool(v) != isNoneKey
	if present == wantPresent {
		return pass(name)
	}
	if wantPresent {
		return fail(name, "expected path to be present and non-null, but it is missing or null")
	}
	return fail(name, fmt.Sprintf("expected path to be missing or null, but got %s", renderGot(got)))
}

func checkContains(name string, expected any, got gjson.Result, negate bool) Result {
	for _, sub := range toStringList(expected) {
		found := valueContains(got, sub)
		switch {
		case negate && found:
			return fail(name, fmt.Sprintf("value unexpectedly contains %q: %s", sub, renderGot(got)))
		case !negate && !found:
			return fail(name, fmt.Sprintf("value does not contain %q: %s", sub, renderGot(got)))
		}
	}
	return pass(name)
}

// valueContains reports a case-insensitive substring match. For arrays, any
// element's string form containing sub counts.
func valueContains(got gjson.Result, sub string) bool {
	subL := strings.ToLower(sub)
	if got.IsArray() {
		for _, el := range got.Array() {
			if strings.Contains(strings.ToLower(el.String()), subL) {
				return true
			}
		}
		return false
	}
	return strings.Contains(strings.ToLower(got.String()), subL)
}

func checkRegex(name string, expected any, got gjson.Result) Result {
	if !got.Exists() {
		return fail(name, "path is missing")
	}
	for _, p := range toStringList(expected) {
		re, err := regexp.Compile(p)
		if err != nil {
			return fail(name, fmt.Sprintf("bad regex %q: %v", p, err))
		}
		if !re.MatchString(got.String()) {
			return fail(name, fmt.Sprintf("value does not match /%s/: %s", p, renderGot(got)))
		}
	}
	return pass(name)
}

func checkStartsWith(name string, v any, got gjson.Result, negate bool) Result {
	if !got.Exists() {
		return fail(name, "path is missing")
	}
	prefix := asString(v)
	has := strings.HasPrefix(got.String(), prefix)
	switch {
	case negate && has:
		return fail(name, fmt.Sprintf("value unexpectedly starts with %q: %s", prefix, renderGot(got)))
	case !negate && !has:
		return fail(name, fmt.Sprintf("value does not start with %q: %s", prefix, renderGot(got)))
	}
	return pass(name)
}

func checkOneOf(name string, v any, got gjson.Result) Result {
	list, ok := v.([]any)
	if !ok {
		return fail(name, fmt.Sprintf("one_of expects a list, got %s", render(v)))
	}
	for _, el := range list {
		if valuesEqual(el, got) {
			return pass(name)
		}
	}
	return fail(name, fmt.Sprintf("expected one of %s, got %s", render(list), renderGot(got)))
}

func checkCompare(name, op string, v any, got gjson.Result) Result {
	threshold, ok := asNumber(v)
	if !ok {
		return fail(name, fmt.Sprintf("%s expects a number, got %s", op, render(v)))
	}
	if got.Type != gjson.Number {
		return fail(name, fmt.Sprintf("expected a number to compare with %s %v, but answer value is %s", op, threshold, jsonKind(got)))
	}
	n := got.Num
	var okCmp bool
	switch op {
	case "gt":
		okCmp = n > threshold
	case "gte":
		okCmp = n >= threshold
	case "lt":
		okCmp = n < threshold
	case "lte":
		okCmp = n <= threshold
	}
	if okCmp {
		return pass(name)
	}
	return fail(name, fmt.Sprintf("expected value %s %v, got %v", op, threshold, n))
}

func checkLength(name, op string, v any, got gjson.Result) Result {
	want, ok := asInt(v)
	if !ok {
		return fail(name, fmt.Sprintf("%s expects an integer, got %s", op, render(v)))
	}
	n, ok := jsonLength(got)
	if !ok {
		return fail(name, fmt.Sprintf("%s needs an array, string, or object, but answer value is %s", op, jsonKind(got)))
	}
	var okCmp bool
	switch op {
	case "length":
		okCmp = n == want
	case "min_length":
		okCmp = n >= want
	case "max_length":
		okCmp = n <= want
	}
	if okCmp {
		return pass(name)
	}
	return fail(name, fmt.Sprintf("expected %s %d, got length %d", op, want, n))
}

// jsonLength returns the element count of an array, key count of an object, or
// rune count of a string. ok is false for other types.
func jsonLength(got gjson.Result) (int, bool) {
	if !got.Exists() {
		return 0, false
	}
	switch {
	case got.IsArray():
		return len(got.Array()), true
	case got.IsObject():
		return len(got.Map()), true
	case got.Type == gjson.String:
		return utf8.RuneCountInString(got.Str), true
	}
	return 0, false
}

// --- value helpers -------------------------------------------------------

func asNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func asInt(v any) (int, bool) {
	if f, ok := asNumber(v); ok {
		return int(f), true
	}
	return 0, false
}

func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		// YAML 1.2 parses `yes`/`on` as strings, not bools; honor the intent.
		switch strings.ToLower(x) {
		case "yes", "y", "on":
			return true
		case "no", "n", "off":
			return false
		}
		b, _ := strconv.ParseBool(x)
		return b
	case int:
		return x != 0
	case float64:
		return x != 0
	}
	return false
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	}
	return fmt.Sprintf("%v", v)
}

// toStringList accepts a single value or a list and returns their string forms.
func toStringList(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, asString(e))
		}
		return out
	case []string:
		return x
	}
	return []string{asString(v)}
}

// jsonKind names got's JSON type for human-readable messages.
func jsonKind(got gjson.Result) string {
	switch {
	case !got.Exists():
		return "missing"
	case got.Type == gjson.Null:
		return "null"
	case got.IsArray():
		return "an array"
	case got.IsObject():
		return "an object"
	case got.Type == gjson.String:
		return "a string"
	case got.Type == gjson.Number:
		return "a number"
	case got.Type == gjson.True, got.Type == gjson.False:
		return "a boolean"
	}
	return "unknown"
}

// render renders an expected value compactly for messages.
func render(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return truncate(fmt.Sprintf("%v", v))
	}
	return truncate(string(b))
}

// renderGot renders the actual JSON value compactly for messages.
func renderGot(got gjson.Result) string {
	if !got.Exists() {
		return "(missing)"
	}
	if b, err := json.Marshal(got.Value()); err == nil {
		return truncate(string(b))
	}
	return truncate(strings.TrimSpace(got.Raw))
}
