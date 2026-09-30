package manage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testHookToken = "hookSECRET_abc-123XYZ"

func TestParseWebhookURL(t *testing.T) {
	tests := []struct {
		raw      string
		wantBase string
		wantErr  string
	}{
		{raw: "https://gptcloud.arc53.com/api/webhooks/agents/" + testHookToken, wantBase: "https://gptcloud.arc53.com"},
		{raw: "  http://localhost:7091/api/webhooks/agents/" + testHookToken + "\n", wantBase: "http://localhost:7091"},
		{raw: "https://example.com/docsgpt/api/webhooks/agents/" + testHookToken, wantBase: "https://example.com/docsgpt"},
		{raw: "", wantErr: "empty"},
		{raw: "ftp://example.com/api/webhooks/agents/" + testHookToken, wantErr: "not an http(s) URL"},
		{raw: "gptcloud.arc53.com/api/webhooks/agents/" + testHookToken, wantErr: "not an http(s) URL"},
		{raw: "https://example.com/api/webhooks/agents/", wantErr: "does not look like an agent webhook"},
		{raw: "https://example.com/api/answer?" + testHookToken, wantErr: "does not look like an agent webhook"},
		{raw: "https://example.com/api/webhooks/agents/" + testHookToken + "/extra", wantErr: "does not look like an agent webhook"},
		{raw: "https://example.com/api/webhooks/agents/" + testHookToken + "?x=1", wantErr: "does not look like an agent webhook"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			w, err := ParseWebhookURL(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), testHookToken) {
					t.Errorf("error leaks the token: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if w.BaseURL() != tt.wantBase {
				t.Errorf("BaseURL = %q, want %q", w.BaseURL(), tt.wantBase)
			}
			if w.URL() != tt.wantBase+WebhookPath+testHookToken {
				t.Errorf("URL = %q", w.URL())
			}
			for _, shown := range []string{w.Redacted(), w.String(), fmt.Sprintf("%v %s", w, w)} {
				if strings.Contains(shown, testHookToken) || !strings.Contains(shown, WebhookPath+"...") {
					t.Errorf("rendered form %q must hide the token", shown)
				}
			}
			if got := w.Redact("boom at " + w.URL()); strings.Contains(got, testHookToken) {
				t.Errorf("Redact = %q", got)
			}
		})
	}
}

func TestWebhookSameOrigin(t *testing.T) {
	w, _ := ParseWebhookURL("https://gptcloud.arc53.com/api/webhooks/agents/" + testHookToken)
	for base, want := range map[string]bool{
		"https://gptcloud.arc53.com":      true,
		"https://GPTCLOUD.arc53.com/":     true,
		"http://gptcloud.arc53.com":       false,
		"https://evil.example.com":        false,
		"https://gptcloud.arc53.com:8443": false,
	} {
		if got := w.SameOrigin(base); got != want {
			t.Errorf("SameOrigin(%q) = %v, want %v", base, got, want)
		}
	}
}

func TestTriggerWebhookRequest(t *testing.T) {
	var gotAuth, gotUA, gotCT, gotKey, gotBody, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotUA, gotCT = r.Header.Get("Authorization"), r.Header.Get("User-Agent"), r.Header.Get("Content-Type")
		gotKey, gotPath = r.Header.Get("Idempotency-Key"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		writeJSONResp(w, 200, `{"success":true,"task_id":"task-9"}`)
	}))
	defer srv.Close()
	hook, err := ParseWebhookURL(srv.URL + WebhookPath + testHookToken)
	if err != nil {
		t.Fatal(err)
	}
	// Even a client that holds a token must not send it to the webhook.
	c := New("https://elsewhere.example", testToken, "docsgpt-cli/test")
	payload := `{"kind":"diagnostic","n":[1,2]}`
	res, err := c.TriggerWebhook(context.Background(), hook, []byte(payload), "pr-12-abc")
	if err != nil {
		t.Fatalf("TriggerWebhook: %v", err)
	}
	if res.TaskID != "task-9" {
		t.Errorf("task id = %q", res.TaskID)
	}
	if gotAuth != "" {
		t.Errorf("the webhook must not get an Authorization header, got %q", gotAuth)
	}
	if gotUA != "docsgpt-cli/test" || gotCT != "application/json" || gotKey != "pr-12-abc" {
		t.Errorf("headers: UA %q, Content-Type %q, Idempotency-Key %q", gotUA, gotCT, gotKey)
	}
	if gotPath != WebhookPath+testHookToken || gotBody != payload {
		t.Errorf("path %q body %q", gotPath, gotBody)
	}

	// No key: no header.
	if _, err := c.TriggerWebhook(context.Background(), hook, []byte(`{}`), ""); err != nil || gotKey != "" {
		t.Errorf("empty key: err %v, header %q", err, gotKey)
	}
	if _, err := c.TriggerWebhook(context.Background(), hook, []byte(`{}`), strings.Repeat("k", IdempotencyKeyMaxLen+1)); err == nil {
		t.Error("an oversized idempotency key must be refused")
	}
}

func TestTriggerWebhookErrorsHideTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "missing"):
			writeJSONResp(w, 404, `{"success":false,"message":"Agent not found"}`)
		case strings.HasSuffix(r.URL.Path, "notask"):
			writeJSONResp(w, 200, `{"success":true}`)
		default:
			writeJSONResp(w, 400, `{"success":false,"message":"Invalid or missing JSON data in request body"}`)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "", "docsgpt-cli/test")
	for token, want := range map[string]string{
		testHookToken + "missing": "404: Agent not found",
		testHookToken + "notask":  "no task_id",
		testHookToken:             "Invalid or missing JSON",
	} {
		hook, _ := ParseWebhookURL(srv.URL + WebhookPath + token)
		_, err := c.TriggerWebhook(context.Background(), hook, []byte(`{}`), "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", token, err, want)
			continue
		}
		if strings.Contains(err.Error(), testHookToken) {
			t.Errorf("error leaks the token: %v", err)
		}
	}

	// A transport failure names the redacted URL only.
	hook, _ := ParseWebhookURL("http://127.0.0.1:1" + WebhookPath + testHookToken)
	_, err := c.TriggerWebhook(context.Background(), hook, []byte(`{}`), "")
	if err == nil || strings.Contains(err.Error(), testHookToken) || !strings.Contains(err.Error(), WebhookPath+"...") {
		t.Errorf("transport error = %v", err)
	}
}

func TestAgentWebhookRerootsOnTheClientBaseURL(t *testing.T) {
	var gotAuth, gotID string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent_webhook" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotAuth, gotID = r.Header.Get("Authorization"), r.URL.Query().Get("id")
		if gotID == "no-scope" {
			writeJSONResp(w, 403, `{"success":false,"error":"insufficient_scope","required_scope":"agents:keys","message":"Token lacks the required scope: agents:keys"}`)
			return
		}
		// The server builds the URL from its API_URL, here a self-hosted default.
		writeJSONResp(w, 200, `{"success":true,"webhook_url":"http://localhost:7091/api/webhooks/agents/`+testHookToken+`"}`)
	})
	hook, err := c.AgentWebhook(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("AgentWebhook: %v", err)
	}
	if gotAuth != "Bearer "+testToken || gotID != "agent-1" {
		t.Errorf("auth %q id %q", gotAuth, gotID)
	}
	if hook.BaseURL() != c.BaseURL || hook.URL() != c.BaseURL+WebhookPath+testHookToken {
		t.Errorf("webhook = %s (base %s), want it on %s", hook, hook.BaseURL(), c.BaseURL)
	}

	_, err = c.AgentWebhook(context.Background(), "no-scope")
	if !IsInsufficientScope(err) || !strings.Contains(err.Error(), "agents:keys") {
		t.Errorf("err = %v", err)
	}
}

func TestAgentRunResult(t *testing.T) {
	tests := []struct {
		name      string
		result    string
		wantErr   string
		wantAns   string
		wantTools int
	}{
		{
			name:    "success",
			result:  `{"status":"success","result":{"answer":"login: arc53-machine","sources":[],"tool_calls":[{"tool_name":"github"},{}],"thought":""}}`,
			wantAns: "login: arc53-machine", wantTools: 2,
		},
		{name: "quota exceeded", result: `{"status":"quota_exceeded","error":"Monthly token quota reached"}`, wantErr: "quota_exceeded): Monthly token quota reached"},
		{name: "idempotency guard", result: `{"success":false,"error":"idempotency poison-loop guard tripped","attempts":6}`, wantErr: "poison-loop"},
		{name: "unknown shape", result: `"done"`},
		{name: "null", result: `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, err := AgentRunResult("task-1", &TaskStatus{Status: "SUCCESS", Result: []byte(tt.result)})
			if tt.wantErr != "" {
				var failed *AgentRunFailedError
				if !errors.As(err, &failed) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if run.Answer != tt.wantAns || run.ToolCalls != tt.wantTools {
				t.Errorf("run = %+v", run)
			}
		})
	}
}
