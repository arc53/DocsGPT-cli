package manage

import (
	"strings"
	"testing"
)

func TestParseResolveErrors(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"source:Docs", "missing '='"},
		{"Docs=abc", "missing '<kind>:' prefix"},
		{"prompt:x=y", "unknown kind"},
		{"source:=abc", "empty selector"},
		{"source:Docs=", "empty value"},
		{"tool:tool-0=maybe", "reuse:<tool-id>, create or skip"},
		{"tool:tool-0=reuse:", "reuse:<tool-id>, create or skip"},
		{"tool:tool-0.secret.=x", "missing secret field name"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			_, err := ParseResolve([]string{tt.in})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParseResolveRedactsSecrets(t *testing.T) {
	set, err := ParseResolve([]string{"tool:brave.secret.token=hunter2", "model:My LLM=sk-live", "model:gpt-x=skip", "sources:Docs=abc"})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range set.Unused() {
		if strings.Contains(raw, "hunter2") || strings.Contains(raw, "sk-live") {
			t.Errorf("secret leaked in %q", raw)
		}
	}
	if set.entries[3].Kind != "source" {
		t.Errorf("plural kind not normalized: %q", set.entries[3].Kind)
	}
}

func samplePlan() *Plan {
	return &Plan{
		Target: PlanTarget{Action: "create"},
		Sources: []PlanSource{
			{Name: "Docs", Status: StatusMatched, TargetID: "s1"},
			{Name: "Handbook", Status: StatusMissing},
		},
		Tools: []PlanTool{
			{Key: "tool-0", Type: "scheduler", Builtin: true, Status: StatusBuiltin},
			{Key: "tool-1", Type: "brave", Name: "search", Status: StatusCreate, RequiresSecrets: []string{"token"}},
			{Key: "tool-2", Type: "legacy_tool", Name: "old", Status: StatusUnavailable},
			{Key: "tool-3", Type: "ghost", Builtin: true, Status: StatusUnavailable},
		},
		Models: []PlanModel{
			{ID: "gpt-x", Status: StatusMatched},
			{ID: "gone-model", Status: StatusUnavailable},
			{DisplayName: "My LLM", Status: StatusCreate, RequiresSecrets: []string{"api_key"}},
		},
	}
}

func blockerRefs(d *Decision) string {
	var out []string
	for _, b := range d.Blockers {
		out = append(out, b.Kind+":"+b.Ref)
	}
	return strings.Join(out, ",")
}

func TestDecideGating(t *testing.T) {
	tests := []struct {
		name         string
		resolve      []string
		wantBlockers string
		wantErr      string
	}{
		{"nothing resolved", nil, "source:Handbook,tool:tool-2,tool:tool-3,model:gone-model", ""},
		{"clean plan entries are ignored", []string{"source:Docs=s9"}, "source:Handbook,tool:tool-2,tool:tool-3,model:gone-model", ""},
		{"source mapped", []string{"source:Handbook=s9"}, "tool:tool-2,tool:tool-3,model:gone-model", ""},
		{"source skipped", []string{"source:Handbook=skip"}, "tool:tool-2,tool:tool-3,model:gone-model", ""},
		{"unavailable tool skipped by key", []string{"tool:tool-2=skip"}, "source:Handbook,tool:tool-3,model:gone-model", ""},
		{"unavailable tool reused by name", []string{"tool:old=reuse:t9"}, "source:Handbook,tool:tool-3,model:gone-model", ""},
		{"create does not cover an unavailable type", []string{"tool:tool-2=create"}, "source:Handbook,tool:tool-2,tool:tool-3,model:gone-model", ""},
		{"reuse does not cover an unavailable builtin", []string{"tool:tool-3=reuse:t9"}, "source:Handbook,tool:tool-2,tool:tool-3,model:gone-model", ""},
		{"builtin skipped by type", []string{"tool:ghost=skip"}, "source:Handbook,tool:tool-2,model:gone-model", ""},
		{"model skipped", []string{"model:gone-model=skip"}, "source:Handbook,tool:tool-2,tool:tool-3", ""},
		{
			"everything covered",
			[]string{"source:Handbook=s9", "tool:tool-2=skip", "tool:tool-3=skip", "model:gone-model=skip"},
			"", "",
		},
		{"api key for a built-in model id", []string{"model:gone-model=sk-1"}, "", "takes no API key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, err := ParseResolve(tt.resolve)
			if err != nil {
				t.Fatal(err)
			}
			d, err := set.Decide(samplePlan())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := blockerRefs(d); got != tt.wantBlockers {
				t.Errorf("blockers = %s, want %s", got, tt.wantBlockers)
			}
			if d.Blocked() != (tt.wantBlockers != "") {
				t.Errorf("Blocked() = %v", d.Blocked())
			}
		})
	}
}

func TestDecideBuildsServerResolution(t *testing.T) {
	set, err := ParseResolve([]string{
		"source:Handbook=s9",
		"source:Docs=skip", // acknowledgement: never sent
		"tool:search.secret.token=hunter2",
		"tool:tool-1=create",
		"tool:tool-2=reuse:t9",
		"tool:tool-3=skip",
		"model:My LLM=sk-live=with=equals",
		"model:gone-model=skip", // acknowledgement: never sent
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := set.Decide(samplePlan())
	if err != nil {
		t.Fatal(err)
	}
	if d.Blocked() {
		t.Fatalf("unexpected blockers: %v", d.Blockers)
	}
	want := `{"sources":{"Handbook":"s9"},` +
		`"tools":{"tool-1":{"decision":"create","secrets":{"token":"hunter2"}},"tool-2":{"decision":"reuse","tool_id":"t9"},"tool-3":{"decision":"skip"}},` +
		`"models":{"My LLM":{"api_key":"sk-live=with=equals"}}}`
	if got := mustJSON(d.Resolution); got != want {
		t.Errorf("resolution\n got %s\nwant %s", got, want)
	}
	if len(set.Unused()) != 0 {
		t.Errorf("unused = %v", set.Unused())
	}
	if len(d.Notices) != 0 {
		t.Errorf("secrets were supplied, no notice expected: %v", d.Notices)
	}
}

func TestDecideNoticesAndEmptyResolution(t *testing.T) {
	set, _ := ParseResolve(nil)
	d, err := set.Decide(samplePlan())
	if err != nil {
		t.Fatal(err)
	}
	if d.Resolution != nil {
		t.Errorf("resolution should be nil without decisions: %+v", d.Resolution)
	}
	joined := strings.Join(d.Notices, "\n")
	for _, want := range []string{"tool-1", "token", `"My LLM"`, "API key"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notices %q lack %q", joined, want)
		}
	}
	// A nil set behaves like an empty one.
	var nilSet *ResolveSet
	if d, err := nilSet.Decide(samplePlan()); err != nil || !d.Blocked() {
		t.Errorf("nil set: %+v, %v", d, err)
	}
}

func TestResolveSelectorsAndUnused(t *testing.T) {
	plan := &Plan{Tools: []PlanTool{
		{Key: "tool-0", Type: "api_tool", Name: "crm", Status: StatusCreate},
		{Key: "tool-1", Type: "api_tool", Name: "billing", Status: StatusCreate},
	}}
	set, _ := ParseResolve([]string{"tool:api_tool=skip"})
	if _, err := set.Decide(plan); err == nil || !strings.Contains(err.Error(), "matches several tools (tool-0, tool-1)") {
		t.Fatalf("ambiguous type: err = %v", err)
	}

	set, _ = ParseResolve([]string{"tool:billing=skip", "tool:tool-7=skip", "source:Nope=s1"})
	d, err := set.Decide(plan)
	if err != nil {
		t.Fatal(err)
	}
	if d.Resolution.Tools["tool-1"].Decision != "skip" || len(d.Resolution.Tools) != 1 {
		t.Errorf("tools = %+v", d.Resolution.Tools)
	}
	if got := strings.Join(set.Unused(), ","); got != "tool:tool-7=skip,source:Nope=s1" {
		t.Errorf("unused = %s", got)
	}
}

func TestCheckDocumentCount(t *testing.T) {
	positional, _ := ParseResolve([]string{"tool:tool-0=skip"})
	named, _ := ParseResolve([]string{"tool:search=skip", "source:Docs=s1"})
	tests := []struct {
		name    string
		set     *ResolveSet
		docs    int
		wantErr bool
	}{
		{"positional, one document", positional, 1, false},
		{"positional, several documents", positional, 2, true},
		{"named, several documents", named, 3, false},
		{"nil set", nil, 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.set.CheckDocumentCount(tt.docs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
