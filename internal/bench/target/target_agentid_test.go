package target

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"docsgpt-cli/internal/bench/spec"

	"github.com/tidwall/gjson"
)

const benchPAT = "dgpt_pat_benchbenchbenchbench"

// TestStreamAndAnswerAuthModes checks both wire shapes of the native targets:
// an agent api_key in the body (no Authorization header, as before), or
// agent_id in the body plus the personal access token as Bearer credential.
func TestStreamAndAnswerAuthModes(t *testing.T) {
	tests := []struct {
		name        string
		target      Target
		req         Request
		wantAuth    string
		wantAPIKey  string
		wantAgentID string
	}{
		{"stream api_key", streamTarget{}, Request{APIKey: "k1"}, "", "k1", ""},
		{"stream agent_id", streamTarget{}, Request{AgentID: "agent-1", Token: benchPAT}, "Bearer " + benchPAT, "", "agent-1"},
		{"answer api_key", answerTarget{}, Request{APIKey: "k1"}, "", "k1", ""},
		{"answer agent_id", answerTarget{}, Request{AgentID: "agent-1", Token: benchPAT}, "Bearer " + benchPAT, "", "agent-1"},
		// A token on its own never changes an api_key request.
		{"stream api_key ignores a stray token", streamTarget{}, Request{APIKey: "k1", Token: benchPAT}, "", "k1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth string
			var gotBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				gotBody, _ = io.ReadAll(r.Body)
				if r.URL.Path == "/stream" {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {\"type\":\"answer\",\"answer\":\"hi\"}\n\ndata: {\"type\":\"end\"}\n\n")
					return
				}
				io.WriteString(w, `{"answer":"hi","conversation_id":"c1"}`)
			}))
			defer srv.Close()

			req := tt.req
			req.Question, req.BaseURL, req.Timeout = "q", srv.URL, 5*time.Second
			res, err := tt.target.Run(context.Background(), req)
			if err != nil || res.Answer != "hi" {
				t.Fatalf("Run: %+v, %v", res, err)
			}
			if gotAuth != tt.wantAuth {
				t.Errorf("Authorization = %q, want %q", gotAuth, tt.wantAuth)
			}
			body := gjson.ParseBytes(gotBody)
			if body.Get("api_key").String() != tt.wantAPIKey || body.Get("api_key").Exists() != (tt.wantAPIKey != "") {
				t.Errorf("api_key in body: %s", gotBody)
			}
			if body.Get("agent_id").String() != tt.wantAgentID || body.Get("agent_id").Exists() != (tt.wantAgentID != "") {
				t.Errorf("agent_id in body: %s", gotBody)
			}
			if strings.Contains(string(gotBody), benchPAT) {
				t.Errorf("the token must travel in the header only: %s", gotBody)
			}
		})
	}
}

func TestAgentIDForbiddenScopeIsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"success":false,"error":"insufficient_scope","message":"Token lacks the required scope","required_scope":"chat:run"}`)
	}))
	defer srv.Close()
	_, err := answerTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, AgentID: "a", Token: benchPAT})
	se, ok := err.(*ServerError)
	if !ok || se.Status != 403 || !strings.Contains(se.Error(), "insufficient_scope: Token lacks the required scope (required scope: chat:run)") {
		t.Fatalf("err = %v", err)
	}
}

func TestV1AndWebhookRejectAgentID(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		req    Request
		want   string
	}{
		{spec.TargetV1, v1Target{}, Request{AgentID: "a", Token: benchPAT, BaseURL: "http://127.0.0.1:1"}, "agent_id is not supported by the v1 target"},
		{spec.TargetWebhook, webhookTarget{}, Request{AgentID: "a", Token: benchPAT, WebhookURL: "http://127.0.0.1:1/hook/tok"}, "agent_id is not supported by the webhook target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.target.Run(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestUploadAttachmentsWithToken(t *testing.T) {
	var gotAuth string
	var hadAPIKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse: %v", err)
		}
		_, hadAPIKey = r.MultipartForm.Value["api_key"]
		io.WriteString(w, `{"success":true,"attachment_id":"att-1"}`)
	}))
	defer srv.Close()
	p := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(p, []byte("x"), 0o644)

	ids, err := UploadAttachmentsWithToken(context.Background(), srv.URL, benchPAT, []string{p}, 5*time.Millisecond)
	if err != nil || len(ids) != 1 || ids[0] != "att-1" {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
	if gotAuth != "Bearer "+benchPAT || hadAPIKey {
		t.Errorf("Authorization = %q, api_key field present = %v", gotAuth, hadAPIKey)
	}

	// The api_key variant is unchanged: form field, no Authorization header.
	if _, err := UploadAttachments(context.Background(), srv.URL, "sk-key", []string{p}, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" || !hadAPIKey {
		t.Errorf("api_key upload: Authorization = %q, api_key field present = %v", gotAuth, hadAPIKey)
	}
}

func TestUploadAttachmentsWithTokenPollsWithToken(t *testing.T) {
	var pollAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/store_attachment":
			io.WriteString(w, `{"success":true,"task_id":"task-1"}`)
		case "/api/task_status":
			pollAuth = r.Header.Get("Authorization")
			io.WriteString(w, `{"status":"SUCCESS","result":{"attachment_id":"att-9"}}`)
		}
	}))
	defer srv.Close()
	p := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(p, []byte("x"), 0o644)
	ids, err := UploadAttachmentsWithToken(context.Background(), srv.URL, benchPAT, []string{p}, 5*time.Millisecond)
	if err != nil || len(ids) != 1 || ids[0] != "att-9" {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
	if pollAuth != "Bearer "+benchPAT {
		t.Errorf("task_status Authorization = %q", pollAuth)
	}
}
