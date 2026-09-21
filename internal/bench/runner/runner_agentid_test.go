package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/bench/spec"
	"github.com/arc53/DocsGPT-cli/internal/bench/target"
)

const runnerPAT = "dgpt_pat_runnerrunnerrunner"

func okTarget() *scriptTarget {
	return &scriptTarget{fn: func(int, target.Request) (*target.Result, error) { return answerResult("ok"), nil }}
}

func TestRunAgentIDCredentialSelection(t *testing.T) {
	tests := []struct {
		name        string
		suite       spec.SuiteConfig
		caseAgent   string
		caseAgentID string
		opts        Options
		wantStatus  Status
		wantErr     string
		wantAPIKey  string
		wantAgentID string
		wantToken   string
		wantLabel   string
	}{
		{
			name: "api key run is unchanged, token is not attached", suite: spec.SuiteConfig{Agent: "a", Target: spec.TargetStream},
			opts: Options{Token: runnerPAT}, wantStatus: StatusPass, wantAPIKey: "KEY:a", wantLabel: "a",
		},
		{
			name: "suite agent_id", suite: spec.SuiteConfig{AgentID: "id-1", Target: spec.TargetStream},
			opts: Options{Token: runnerPAT}, wantStatus: StatusPass, wantAgentID: "id-1", wantToken: runnerPAT, wantLabel: "agent:id-1",
		},
		{
			name: "case agent_id on the answer target", suite: spec.SuiteConfig{Agent: "a", Target: spec.TargetAnswer}, caseAgentID: "id-2",
			opts: Options{Token: runnerPAT}, wantStatus: StatusPass, wantAgentID: "id-2", wantToken: runnerPAT, wantLabel: "agent:id-2",
		},
		{
			name: "--agent-id overrides the YAML agent", suite: spec.SuiteConfig{Agent: "a", Target: spec.TargetStream},
			opts: Options{Token: runnerPAT, AgentIDOverride: "id-9"}, wantStatus: StatusPass, wantAgentID: "id-9", wantToken: runnerPAT, wantLabel: "agent:id-9",
		},
		{
			name: "--key overrides the YAML agent_id", suite: spec.SuiteConfig{AgentID: "id-1", Target: spec.TargetStream},
			opts: Options{Token: runnerPAT, AgentOverride: "other"}, wantStatus: StatusPass, wantAPIKey: "KEY:other", wantLabel: "other",
		},
		{
			name: "agent_id without a token", suite: spec.SuiteConfig{AgentID: "id-1", Target: spec.TargetStream},
			wantStatus: StatusError, wantErr: "requires a personal access token",
		},
		{
			name: "agent_id with the v1 target", suite: spec.SuiteConfig{AgentID: "id-1", Target: spec.TargetStream},
			opts: Options{Token: runnerPAT, TargetOverride: spec.TargetV1}, wantStatus: StatusError, wantErr: "agent_id is not supported by the v1 target",
		},
		{
			name: "agent_id with the webhook target", suite: spec.SuiteConfig{AgentID: "id-1", Target: spec.TargetWebhook, WebhookURL: "http://x/hook"},
			opts: Options{Token: runnerPAT}, wantStatus: StatusError, wantErr: "agent_id is not supported by the webhook target",
		},
		{
			name: "suite base_url on another origin never receives the token", suite: spec.SuiteConfig{AgentID: "id-1", Target: spec.TargetStream, BaseURL: "https://evil.example.com"},
			opts: Options{Token: runnerPAT}, wantStatus: StatusError, wantErr: "refusing to send the personal access token to https://evil.example.com",
		},
		{
			name: "--url makes another origin trusted", suite: spec.SuiteConfig{AgentID: "id-1", Target: spec.TargetStream, BaseURL: "https://evil.example.com"},
			opts: Options{Token: runnerPAT, URLOverride: "https://staging.example.com"}, wantStatus: StatusPass, wantAgentID: "id-1", wantToken: runnerPAT, wantLabel: "agent:id-1",
		},
		{
			name: "suite base_url on another origin is fine for api key runs", suite: spec.SuiteConfig{Agent: "a", Target: spec.TargetStream, BaseURL: "https://other.example.com"},
			opts: Options{Token: runnerPAT}, wantStatus: StatusPass, wantAPIKey: "KEY:a", wantLabel: "a",
		},
		{
			name: "neither configured", suite: spec.SuiteConfig{Target: spec.TargetStream},
			opts: Options{Token: runnerPAT}, wantStatus: StatusError, wantErr: "no agent configured",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tg := okTarget()
			withTarget(t, tg)
			c := containsCase("c", "ok")
			c.Agent, c.AgentID = tt.caseAgent, tt.caseAgentID
			suite := newSuite(tt.suite, c)

			opts := tt.opts
			opts.Suite, opts.Cases, opts.ResolveKey, opts.BaseURL = suite, suite.Cases, defaultResolver, "http://x"
			sr, err := Run(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			cr := sr.Cases[0]
			if cr.Status != tt.wantStatus {
				t.Fatalf("status = %s (%+v), want %s", cr.Status, cr.Runs[0], tt.wantStatus)
			}
			if tt.wantErr != "" {
				if !strings.Contains(cr.Runs[0].Error, tt.wantErr) {
					t.Errorf("error = %q, want %q", cr.Runs[0].Error, tt.wantErr)
				}
				if len(tg.reqs) != 0 {
					t.Errorf("no request should be sent, got %d", len(tg.reqs))
				}
				return
			}
			req := tg.reqs[0]
			if req.APIKey != tt.wantAPIKey || req.AgentID != tt.wantAgentID || req.Token != tt.wantToken {
				t.Errorf("request APIKey=%q AgentID=%q Token=%q, want %q / %q / %q",
					req.APIKey, req.AgentID, req.Token, tt.wantAPIKey, tt.wantAgentID, tt.wantToken)
			}
			if sr.AgentLabel != tt.wantLabel {
				t.Errorf("label = %q, want %q", sr.AgentLabel, tt.wantLabel)
			}
		})
	}
}

func TestRunRejectsBothOverrides(t *testing.T) {
	suite := newSuite(spec.SuiteConfig{Agent: "a"}, containsCase("c", "ok"))
	_, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, AgentOverride: "k", AgentIDOverride: "id",
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunAgentIDUploadsAttachmentsWithToken(t *testing.T) {
	tg := okTarget()
	withTarget(t, tg)
	var keyUploads, tokenUploads int
	var gotToken string
	oldKey, oldTok := uploadAttachments, uploadAttachmentsWithToken
	uploadAttachments = func(context.Context, string, string, []string, time.Duration) ([]string, error) {
		keyUploads++
		return []string{"by-key"}, nil
	}
	uploadAttachmentsWithToken = func(_ context.Context, _, token string, _ []string, _ time.Duration) ([]string, error) {
		tokenUploads++
		gotToken = token
		return []string{"by-token"}, nil
	}
	t.Cleanup(func() { uploadAttachments, uploadAttachmentsWithToken = oldKey, oldTok })

	c := containsCase("c", "ok")
	c.Dir = t.TempDir()
	c.Attachments = spec.StringList{"f.pdf"}
	suite := newSuite(spec.SuiteConfig{AgentID: "id-1", Target: spec.TargetStream}, c)
	sr, err := Run(context.Background(), Options{
		Suite: suite, Cases: suite.Cases, ResolveKey: defaultResolver, BaseURL: "http://x", Token: runnerPAT,
	})
	if err != nil || sr.Cases[0].Status != StatusPass {
		t.Fatalf("run: %+v, %v", sr.Cases[0].Runs[0], err)
	}
	if tokenUploads != 1 || keyUploads != 0 || gotToken != runnerPAT {
		t.Errorf("token uploads = %d, key uploads = %d, token = %q", tokenUploads, keyUploads, gotToken)
	}
	if got := tg.reqs[0].AttachmentIDs; len(got) != 1 || got[0] != "by-token" {
		t.Errorf("attachment ids = %v", got)
	}
}
