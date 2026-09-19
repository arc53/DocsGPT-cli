package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const expectHello = "expect:\n  answer: {contains: hello}\n"

func TestEffectiveAgentOrAgentID(t *testing.T) {
	tests := []struct {
		name        string
		suite       SuiteConfig
		c           Case
		wantAgent   string
		wantAgentID string
	}{
		{"suite agent (unchanged behaviour)", SuiteConfig{Agent: "key-a"}, Case{}, "key-a", ""},
		{"case agent overrides suite agent", SuiteConfig{Agent: "key-a"}, Case{Agent: "key-b"}, "key-b", ""},
		{"suite agent_id", SuiteConfig{AgentID: "id-1"}, Case{}, "", "id-1"},
		{"case agent_id overrides suite agent_id", SuiteConfig{AgentID: "id-1"}, Case{AgentID: "id-2"}, "", "id-2"},
		{"case agent_id replaces suite agent", SuiteConfig{Agent: "key-a"}, Case{AgentID: "id-2"}, "", "id-2"},
		{"case agent replaces suite agent_id", SuiteConfig{AgentID: "id-1"}, Case{Agent: "key-b"}, "key-b", ""},
		{"nothing set", SuiteConfig{}, Case{}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Suite{Config: tt.suite}
			eff := s.Effective(&tt.c)
			if eff.Agent != tt.wantAgent || eff.AgentID != tt.wantAgentID {
				t.Errorf("Agent=%q AgentID=%q, want %q / %q", eff.Agent, eff.AgentID, tt.wantAgent, tt.wantAgentID)
			}
		})
	}
}

func TestLoadAgentIDValidation(t *testing.T) {
	tests := []struct {
		name, suiteYAML, caseYAML, wantErr string
	}{
		{"stream + suite agent_id", "agent_id: id-1\ntarget: stream\n", "question: hi\n" + expectHello, ""},
		{"answer + case agent_id", "agent: key-a\n", "question: hi\ntarget: answer\nagent_id: id-2\n" + expectHello, ""},
		{"case agent escapes the suite agent_id for v1", "agent_id: id-1\ntarget: stream\n", "question: hi\ntarget: v1\nagent: key-a\n" + expectHello, ""},
		{"skipped case is not validated", "agent_id: id-1\n", "skip: later\n", ""},
		{"v1 (default target) + agent_id", "agent_id: id-1\n", "question: hi\n" + expectHello, "agent_id is not supported by the v1 target"},
		{"webhook + agent_id", "", "question: hi\ntarget: webhook\nwebhook_url: http://x/y\nagent_id: id-1\n" + expectHello, "agent_id is not supported by the webhook target"},
		{"both on the case", "", "question: hi\ntarget: stream\nagent: key-a\nagent_id: id-1\n" + expectHello, "agent and agent_id are mutually exclusive"},
		{"both on the suite", "agent: key-a\nagent_id: id-1\ntarget: stream\n", "question: hi\n" + expectHello, "agent and agent_id are mutually exclusive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.suiteYAML != "" {
				if err := os.WriteFile(filepath.Join(root, SuiteFileName), []byte(tt.suiteYAML), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			writeCase(t, root, "01-case", tt.caseYAML)
			_, err := Load(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestAgentIDFromEnvInterpolation(t *testing.T) {
	t.Setenv("BENCH_AGENT_ID", "id-from-env")
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, SuiteFileName), []byte("agent_id: ${BENCH_AGENT_ID}\ntarget: stream\n"), 0o644)
	writeCase(t, root, "01-case", "question: hi\n"+expectHello)
	s, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Effective(s.Cases[0]).AgentID; got != "id-from-env" {
		t.Errorf("AgentID = %q", got)
	}
}
