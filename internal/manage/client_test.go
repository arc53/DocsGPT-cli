package manage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "dgpt_pat_abcdefSECRETSECRETSECRET"

// newTestClient returns a client for an httptest server running h.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", testToken, "docsgpt-cli/test")
}

func writeJSONResp(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

func TestClientSendsAuthAndUserAgent(t *testing.T) {
	var gotAuth, gotUA, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotUA, gotPath = r.Header.Get("Authorization"), r.Header.Get("User-Agent"), r.URL.Path
		writeJSONResp(w, 200, `{"success":true,"user_id":"u1","roles":["user"],"auth_method":"pat",
			"token":{"id":"t1","name":"ci","scopes":["agents:read","agents:write"],"resource_filter":{"agents":["a1"]}}}`)
	})
	id, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if gotAuth != "Bearer "+testToken {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotUA != "docsgpt-cli/test" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if gotPath != "/api/user/me" {
		t.Errorf("path = %q (trailing slash of the base URL must be trimmed)", gotPath)
	}
	if id.UserID != "u1" || id.AuthMethod != "pat" || id.Token == nil || id.Token.Name != "ci" {
		t.Fatalf("identity = %+v", id)
	}
	if len(id.Token.Scopes) != 2 || id.Token.ResourceFilter["agents"][0] != "a1" {
		t.Errorf("token = %+v", id.Token)
	}
	if !json.Valid(id.Raw) {
		t.Errorf("Raw is not the server document: %s", id.Raw)
	}
}

func TestAPIErrorDecoding(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantCode  string
		wantScope string
		contains  []string
		check     func(error) bool
	}{
		{
			name: "insufficient scope with required_scope", status: 403,
			body:     `{"success":false,"error":"insufficient_scope","message":"Token lacks the required scope: agents:write","required_scope":"agents:write"}`,
			wantCode: CodeInsufficientScope, wantScope: "agents:write",
			contains: []string{`"agents:write"`, "Token lacks the required scope: agents:write"},
			check:    IsInsufficientScope,
		},
		{
			name: "insufficient scope without required_scope", status: 403,
			body:     `{"error":"insufficient_scope","message":"Token lacks the required scope"}`,
			wantCode: CodeInsufficientScope,
			contains: []string{"lacks a required scope"},
			check:    IsInsufficientScope,
		},
		{
			name: "resource not allowed", status: 403,
			body:     `{"error":"resource_not_allowed","message":"agent not in token filter"}`,
			wantCode: CodeResourceNotAllowed,
			contains: []string{"resource restrictions", "agent not in token filter"},
			check:    IsResourceNotAllowed,
		},
		{
			name: "endpoint closed to tokens", status: 403,
			body:     `{"success":false,"error":"not_available_to_tokens","message":"This endpoint cannot be called with a personal access token"}`,
			wantCode: CodeNotForTokens,
			contains: []string{"cannot be called with a personal access token"},
		},
		{
			name: "invalid token", status: 401,
			body:     `{"message":"Authentication error: invalid token","error":"invalid_token"}`,
			wantCode: CodeInvalidToken,
			contains: []string{"invalid, expired or revoked", "docsgpt-cli login"},
			check:    IsUnauthorized,
		},
		{
			name: "bare 401", status: 401, body: `{"success":false}`,
			contains: []string{"authentication failed"},
			check:    IsUnauthorized,
		},
		{
			name: "server message", status: 400,
			body:     `{"success":false,"message":"Unsupported kind 'Tool'; expected 'Agent'"}`,
			contains: []string{"returned 400", "Unsupported kind 'Tool'"},
		},
		{
			name: "legacy status not found", status: 404, body: `{"status":"not found"}`,
			contains: []string{"returned 404", "not found"},
			check:    IsNotFound,
		},
		{
			name: "non-JSON body", status: 502, body: "<html>bad gateway</html>",
			contains: []string{"returned 502", "bad gateway"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSONResp(w, tt.status, tt.body)
			})
			_, _, err := c.ListAgents(context.Background())
			var ae *APIError
			if !errors.As(err, &ae) {
				t.Fatalf("error = %v (%T), want *APIError", err, err)
			}
			if ae.Status != tt.status || ae.Code != tt.wantCode || ae.RequiredScope != tt.wantScope {
				t.Errorf("APIError = %+v", ae)
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Errorf("error leaks the token: %q", err.Error())
			}
			if tt.check != nil && !tt.check(err) {
				t.Errorf("predicate is false for %v", err)
			}
		})
	}
}

func TestSuccessFalseOn200IsAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSONResp(w, 200, `{"success":false,"message":"nope"}`)
	})
	if _, err := c.Me(context.Background()); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientTimeout(t *testing.T) {
	release := make(chan struct{})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	c.Timeout = 30 * time.Millisecond
	start := time.Now()
	_, err := c.Me(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("timeout not applied: %s", time.Since(start))
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error leaks the token: %q", err)
	}
}

func TestListEndpoints(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/get_agents":
			writeJSONResp(w, 200, `[{"id":"a1","name":"Support","slug":"support","agent_type":"classic","status":"published","ownership":"user","extra":1}]`)
		case "/api/sources":
			writeJSONResp(w, 200, `[{"id":"s1","name":"Docs","tokens":1234,"type":"file","date":"2026-01-01","ownership":"user"}]`)
		case "/api/get_prompts":
			writeJSONResp(w, 200, `[{"id":"default","name":"default","type":"public"},{"id":"p1","name":"mine","type":"private"}]`)
		case "/api/get_tools":
			writeJSONResp(w, 200, `{"success":true,"tools":[{"id":"t1","name":"brave","customName":"search","displayName":"Brave","status":true}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	ctx := context.Background()

	agents, raw, err := c.ListAgents(ctx)
	if err != nil || len(agents) != 1 || agents[0].Slug != "support" || agents[0].Status != "published" {
		t.Fatalf("agents = %+v, err = %v", agents, err)
	}
	if !strings.Contains(string(raw), `"extra":1`) {
		t.Errorf("raw should pass unknown fields through: %s", raw)
	}
	sources, _, err := c.ListSources(ctx)
	if err != nil || len(sources) != 1 || sources[0].Name != "Docs" {
		t.Fatalf("sources = %+v, err = %v", sources, err)
	}
	prompts, _, err := c.ListPrompts(ctx)
	if err != nil || len(prompts) != 2 || prompts[1].Type != "private" {
		t.Fatalf("prompts = %+v, err = %v", prompts, err)
	}
	tools, rawTools, err := c.ListTools(ctx)
	if err != nil || len(tools) != 1 || tools[0].CustomName != "search" || !tools[0].Status {
		t.Fatalf("tools = %+v, err = %v", tools, err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(rawTools)), "[") {
		t.Errorf("raw tools should be the tools array: %s", rawTools)
	}
}

func TestExportAndDeleteAgent(t *testing.T) {
	const doc = "apiVersion: docsgpt.arc53.com/v1\nkind: Agent\n"
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/export_agent":
			if r.URL.Query().Get("id") != "a 1" {
				t.Errorf("id = %q", r.URL.Query().Get("id"))
			}
			w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
			io.WriteString(w, doc)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/delete_agent":
			if r.URL.Query().Get("id") == "gone" {
				writeJSONResp(w, 404, `{"success":false,"message":"Agent not found"}`)
				return
			}
			writeJSONResp(w, 200, `{"id":"a1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/delete_old":
			if r.URL.Query().Get("source_id") != "s1" {
				t.Errorf("source_id = %q", r.URL.Query().Get("source_id"))
			}
			writeJSONResp(w, 200, `{"success":true}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	ctx := context.Background()
	got, err := c.ExportAgent(ctx, "a 1")
	if err != nil || string(got) != doc {
		t.Fatalf("export = %q, err = %v", got, err)
	}
	if err := c.DeleteAgent(ctx, "a1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := c.DeleteAgent(ctx, "gone"); !IsNotFound(err) || !strings.Contains(err.Error(), "Agent not found") {
		t.Fatalf("delete gone: %v", err)
	}
	if err := c.DeleteSource(ctx, "s1"); err != nil {
		t.Fatalf("delete source: %v", err)
	}
}

func TestPlanAndApplyRequestShapes(t *testing.T) {
	const doc = "kind: Agent\n# comment survives\n"
	var planBody, applyBody map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			t.Errorf("%s %s content-type %q", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/api/import_agent/plan":
			json.Unmarshal(raw, &planBody)
			writeJSONResp(w, 200, `{"success":true,"plan":{
				"target":{"action":"update","agent_id":"a1","matched_by":"slug","status":"published"},
				"sources":[{"name":"Docs","type":"file","status":"matched","target_id":"s1"},{"name":"Handbook","type":"file","status":"missing","target_id":null}],
				"tools":[{"key":"tool-0","type":"scheduler","builtin":true,"status":"builtin","target_id":"d1"},
				         {"key":"tool-1","type":"brave","name":"search","status":"create","requires_secrets":["token"]}],
				"prompt":{"status":"reuse","name":"support"},
				"models":[{"id":"gpt-x","status":"matched"},{"display_name":"My LLM","status":"create","requires_secrets":["api_key"]}],
				"workflow":null}}`)
		case "/api/import_agent":
			applyBody = nil
			json.Unmarshal(raw, &applyBody)
			writeJSONResp(w, 200, `{"success":true,"agent_id":"a1","action":"updated","status":"published","agent_type":"classic","slug":"support","warnings":["w1"]}`)
		}
	})
	ctx := context.Background()

	plan, err := c.PlanImport(ctx, doc)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if planBody["yaml"] != doc || len(planBody) != 1 {
		t.Errorf("plan body = %v, want only {yaml}", planBody)
	}
	if plan.Target.Action != "update" || plan.Target.MatchedBy != "slug" || len(plan.Sources) != 2 ||
		plan.Sources[1].Status != StatusMissing || plan.Tools[1].RequiresSecrets[0] != "token" ||
		plan.Models[1].Label() != "My LLM" || plan.Workflow != nil || len(plan.Raw) == 0 {
		t.Errorf("plan = %+v", plan)
	}

	res := &Resolution{
		Sources: map[string]string{"Handbook": "s9"},
		Tools:   map[string]ToolDecision{"tool-1": {Decision: "create", Secrets: map[string]string{"token": "sek"}}},
		Models:  map[string]ModelDecision{"My LLM": {APIKey: "k"}},
	}
	out, err := c.ApplyImport(ctx, doc, res)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out.Action != "updated" || out.AgentID != "a1" || len(out.Warnings) != 1 {
		t.Errorf("result = %+v", out)
	}
	want := map[string]any{
		"yaml": doc,
		"resolution": map[string]any{
			"sources": map[string]any{"Handbook": "s9"},
			"tools":   map[string]any{"tool-1": map[string]any{"decision": "create", "secrets": map[string]any{"token": "sek"}}},
			"models":  map[string]any{"My LLM": map[string]any{"api_key": "k"}},
		},
	}
	if g, w := mustJSON(applyBody), mustJSON(want); g != w {
		t.Errorf("apply body\n got %s\nwant %s", g, w)
	}

	// No decisions: the resolution key is omitted entirely.
	if _, err := c.ApplyImport(ctx, doc, &Resolution{}); err != nil {
		t.Fatal(err)
	}
	if _, present := applyBody["resolution"]; present {
		t.Errorf("empty resolution should be omitted: %v", applyBody)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
