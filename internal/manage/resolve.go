package manage

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A --resolve entry is `<kind>:<selector>=<value>` and maps onto the server's
// `resolution` object (see Resolution):
//
//	source:<name>=<source-id>          resolution.sources[name] = id
//	source:<name>=skip                 acknowledge: leave the source unattached
//	tool:<sel>=reuse:<tool-id>         resolution.tools[key] = {decision: reuse, tool_id}
//	tool:<sel>=create                  resolution.tools[key] = {decision: create}
//	tool:<sel>=skip                    resolution.tools[key] = {decision: skip}
//	tool:<sel>.secret.<field>=<value>  resolution.tools[key].secrets[field] = value
//	model:<display-name>=<api-key>     resolution.models[name] = {api_key}
//	model:<id-or-name>=skip            acknowledge: drop the model from the agent
//
// <sel> is the plan key (tool-0, tool-1, … = position in spec.tools), or the
// tool's name or type when that is unambiguous within the document.
//
// The server has no "skip" for sources and models — an unresolved one is
// simply left off the agent with a warning — so those two `skip` forms are
// acknowledgements handled by the CLI's gate and are never sent.
const skipValue = "skip"

// Resolve entry kinds.
const (
	resolveSource = "source"
	resolveTool   = "tool"
	resolveModel  = "model"
)

var toolKeyRe = regexp.MustCompile(`^tool-\d+$`)

// ResolveEntry is one parsed --resolve flag.
type ResolveEntry struct {
	Raw      string // the flag value with any secret redacted, for messages
	Kind     string // source | tool | model
	Selector string
	Secret   string // tool secret field name ("" for a decision entry)
	Value    string

	used bool
}

// ResolveSet is the parsed --resolve flags for one command invocation.
type ResolveSet struct {
	entries []*ResolveEntry
}

// ParseResolve parses --resolve flag values.
func ParseResolve(flags []string) (*ResolveSet, error) {
	set := &ResolveSet{}
	for _, raw := range flags {
		e, err := parseResolveEntry(raw)
		if err != nil {
			return nil, err
		}
		set.entries = append(set.entries, e)
	}
	return set, nil
}

func parseResolveEntry(raw string) (*ResolveEntry, error) {
	const usage = "expected source:<name>=<id|skip>, tool:<key>=<reuse:<id>|create|skip>, tool:<key>.secret.<field>=<value> or model:<name>=<api-key|skip>"
	eq := strings.Index(raw, "=")
	if eq < 0 {
		return nil, fmt.Errorf("invalid --resolve %q: missing '=' (%s)", raw, usage)
	}
	key, value := strings.TrimSpace(raw[:eq]), raw[eq+1:]
	kind, selector, ok := strings.Cut(key, ":")
	if !ok {
		return nil, fmt.Errorf("invalid --resolve %q: missing '<kind>:' prefix (%s)", key, usage)
	}
	kind = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(kind)), "s") // sources: -> source
	selector = strings.TrimSpace(selector)
	e := &ResolveEntry{Raw: raw, Kind: kind, Selector: selector, Value: value}

	switch kind {
	case resolveSource:
		e.Value = strings.TrimSpace(value)
	case resolveModel:
		e.Value = strings.TrimSpace(value)
		if e.Value != skipValue {
			e.Raw = key + "=<redacted>"
		}
	case resolveTool:
		if i := strings.LastIndex(selector, ".secret."); i >= 0 {
			e.Selector, e.Secret = selector[:i], selector[i+len(".secret."):]
			e.Raw = key + "=<redacted>"
			if e.Secret == "" {
				return nil, fmt.Errorf("invalid --resolve %q: missing secret field name", key)
			}
			break
		}
		e.Value = strings.TrimSpace(value)
		switch {
		case e.Value == skipValue, e.Value == "create":
		case strings.HasPrefix(e.Value, "reuse:") && strings.TrimSpace(e.Value[len("reuse:"):]) != "":
		default:
			return nil, fmt.Errorf("invalid --resolve %q: a tool decision is reuse:<tool-id>, create or skip", raw)
		}
	default:
		return nil, fmt.Errorf("invalid --resolve %q: unknown kind %q (%s)", key, kind, usage)
	}
	if e.Selector == "" {
		return nil, fmt.Errorf("invalid --resolve %q: empty selector (%s)", e.Raw, usage)
	}
	if e.Value == "" {
		return nil, fmt.Errorf("invalid --resolve %q: empty value", e.Raw)
	}
	return e, nil
}

// CheckDocumentCount rejects positional tool keys when several documents are
// processed together: tool-0 would silently address a different tool in every
// document.
func (s *ResolveSet) CheckDocumentCount(n int) error {
	if s == nil || n <= 1 {
		return nil
	}
	for _, e := range s.entries {
		if e.Kind == resolveTool && toolKeyRe.MatchString(e.Selector) {
			return fmt.Errorf("--resolve %q uses a positional tool key, which is ambiguous across %d documents: select the tool by name or type, or apply the documents one at a time", e.Raw, n)
		}
	}
	return nil
}

// Unused lists the entries that matched nothing in any planned document.
func (s *ResolveSet) Unused() []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, e := range s.entries {
		if !e.used {
			out = append(out, e.Raw)
		}
	}
	return out
}

// Blocker is one unresolved reference that stops an apply.
type Blocker struct {
	Kind   string `json:"kind"` // source | tool | model
	Ref    string `json:"ref"`  // source name, tool key, model id/name
	Status string `json:"status"`
	Hint   string `json:"hint"`
}

func (b Blocker) String() string {
	return fmt.Sprintf("%s %q is %s — %s", b.Kind, b.Ref, b.Status, b.Hint)
}

// Decision is the outcome of matching the --resolve entries against one
// document's plan.
type Decision struct {
	Resolution *Resolution // what to send with the apply (nil when empty)
	Blockers   []Blocker   // missing / unavailable references left uncovered
	Notices    []string    // non-blocking heads-ups
	// Covered maps "kind:ref" to a short description of the covering entry,
	// for the plan summary.
	Covered map[string]string
}

// Blocked reports whether the apply must not proceed.
func (d *Decision) Blocked() bool { return len(d.Blockers) > 0 }

// Decide matches the entries against plan. It builds the resolution to send
// and lists every `missing` / `unavailable` reference that no entry covers:
//
//   - a missing source is covered by source:<name>=<id> or =skip;
//   - an unavailable tool is covered by =skip, or (non-built-in only, since the
//     server ignores decisions for built-ins) by =reuse:<tool-id>;
//   - an unavailable model id is covered by =skip.
//
// An error means the entries are invalid for this plan (ambiguous selector).
func (s *ResolveSet) Decide(plan *Plan) (*Decision, error) {
	d := &Decision{Covered: map[string]string{}}
	res := &Resolution{
		Sources: map[string]string{},
		Tools:   map[string]ToolDecision{},
		Models:  map[string]ModelDecision{},
	}
	ackSources := map[string]bool{}
	ackModels := map[string]bool{}

	var entries []*ResolveEntry
	if s != nil {
		entries = s.entries
	}
	for _, e := range entries {
		switch e.Kind {
		case resolveSource:
			for _, src := range plan.Sources {
				if src.Name != e.Selector {
					continue
				}
				e.used = true
				if e.Value == skipValue {
					ackSources[src.Name] = true
					d.Covered["source:"+src.Name] = "left unattached (skip)"
				} else {
					res.Sources[src.Name] = e.Value
					d.Covered["source:"+src.Name] = "mapped to " + e.Value
				}
			}
		case resolveModel:
			for _, m := range plan.Models {
				if m.Label() != e.Selector {
					continue
				}
				e.used = true
				switch {
				case e.Value == skipValue:
					ackModels[m.Label()] = true
					d.Covered["model:"+m.Label()] = "dropped (skip)"
				case m.DisplayName != "":
					res.Models[m.DisplayName] = ModelDecision{APIKey: e.Value}
					d.Covered["model:"+m.Label()] = "API key supplied"
				default:
					return nil, fmt.Errorf("--resolve %q: %q is a built-in model id, which takes no API key (use =skip to drop it)", e.Raw, m.ID)
				}
			}
		case resolveTool:
			key, err := matchTool(plan.Tools, e)
			if err != nil {
				return nil, err
			}
			if key == "" {
				continue
			}
			e.used = true
			td := res.Tools[key]
			switch {
			case e.Secret != "":
				if td.Secrets == nil {
					td.Secrets = map[string]string{}
				}
				td.Secrets[e.Secret] = e.Value
			case strings.HasPrefix(e.Value, "reuse:"):
				td.Decision = "reuse"
				td.ToolID = strings.TrimSpace(e.Value[len("reuse:"):])
			default:
				td.Decision = e.Value // create | skip
			}
			res.Tools[key] = td
		}
	}

	for _, src := range plan.Sources {
		if src.Status != StatusMissing {
			continue
		}
		if res.Sources[src.Name] != "" || ackSources[src.Name] {
			continue
		}
		d.Blockers = append(d.Blockers, Blocker{
			Kind: resolveSource, Ref: src.Name, Status: src.Status,
			Hint: fmt.Sprintf("upload it first, or pass --resolve %q (or =skip to leave it unattached)", "source:"+src.Name+"=<source-id>"),
		})
	}
	for _, t := range plan.Tools {
		td, decided := res.Tools[t.Key]
		if decided {
			d.Covered["tool:"+t.Key] = describeToolDecision(td)
		}
		if t.Status == StatusUnavailable {
			covered := td.Decision == skipValue || (!t.Builtin && td.Decision == "reuse" && td.ToolID != "")
			if !covered {
				hint := fmt.Sprintf("the tool type %q does not exist on this server; pass --resolve %q", t.Type, "tool:"+t.Key+"=skip")
				if !t.Builtin {
					hint += fmt.Sprintf(" or --resolve %q", "tool:"+t.Key+"=reuse:<tool-id>")
				}
				d.Blockers = append(d.Blockers, Blocker{Kind: resolveTool, Ref: t.Key, Status: t.Status, Hint: hint})
			}
			continue
		}
		if t.Status == StatusCreate && td.Decision != skipValue && td.Decision != "reuse" {
			if missing := missingSecrets(t.RequiresSecrets, td.Secrets); len(missing) > 0 {
				d.Notices = append(d.Notices, fmt.Sprintf(
					"tool %s (%s) may need secret(s) %s to be created; without them the server skips the tool — pass --resolve %q",
					t.Key, toolLabel(t), strings.Join(missing, ", "), "tool:"+t.Key+".secret."+missing[0]+"=<value>"))
			}
		}
	}
	for _, m := range plan.Models {
		switch {
		case m.Status == StatusUnavailable:
			if ackModels[m.Label()] {
				continue
			}
			d.Blockers = append(d.Blockers, Blocker{
				Kind: resolveModel, Ref: m.Label(), Status: m.Status,
				Hint: fmt.Sprintf("the model is not available on this server; pass --resolve %q to drop it", "model:"+m.Label()+"=skip"),
			})
		case m.Status == StatusCreate && m.DisplayName != "" && !ackModels[m.Label()]:
			if res.Models[m.DisplayName].APIKey == "" {
				d.Notices = append(d.Notices, fmt.Sprintf(
					"custom model %q needs an API key to be created; without it the server skips the model — pass --resolve %q",
					m.DisplayName, "model:"+m.DisplayName+"=<api-key>"))
			}
		}
	}

	if !res.Empty() {
		if len(res.Sources) == 0 {
			res.Sources = nil
		}
		if len(res.Tools) == 0 {
			res.Tools = nil
		}
		if len(res.Models) == 0 {
			res.Models = nil
		}
		d.Resolution = res
	}
	return d, nil
}

// matchTool resolves a tool selector to a plan key ("" = no match here).
func matchTool(tools []PlanTool, e *ResolveEntry) (string, error) {
	if toolKeyRe.MatchString(e.Selector) {
		for _, t := range tools {
			if t.Key == e.Selector {
				return t.Key, nil
			}
		}
		return "", nil
	}
	var keys []string
	for _, t := range tools {
		if t.Name != "" && t.Name == e.Selector {
			keys = append(keys, t.Key)
		}
	}
	if len(keys) == 0 { // fall back to the tool type
		for _, t := range tools {
			if t.Type == e.Selector {
				keys = append(keys, t.Key)
			}
		}
	}
	switch len(keys) {
	case 0:
		return "", nil
	case 1:
		return keys[0], nil
	}
	sort.Strings(keys)
	return "", fmt.Errorf("--resolve %q: %q matches several tools (%s); use the tool-N key", e.Raw, e.Selector, strings.Join(keys, ", "))
}

func missingSecrets(required []string, given map[string]string) []string {
	var out []string
	for _, r := range required {
		if given[r] == "" {
			out = append(out, r)
		}
	}
	return out
}

func toolLabel(t PlanTool) string {
	if t.Name != "" {
		return t.Type + " " + fmt.Sprintf("%q", t.Name)
	}
	return t.Type
}

func describeToolDecision(td ToolDecision) string {
	var parts []string
	switch td.Decision {
	case "reuse":
		parts = append(parts, "reuse "+td.ToolID)
	case "":
	default:
		parts = append(parts, td.Decision)
	}
	if n := len(td.Secrets); n > 0 {
		parts = append(parts, fmt.Sprintf("%d secret(s) supplied", n))
	}
	return strings.Join(parts, ", ")
}
