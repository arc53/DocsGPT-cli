package target

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// ForName
// ---------------------------------------------------------------------------

func TestForName(t *testing.T) {
	for name, want := range map[string]string{"v1": "v1", "stream": "stream", "webhook": "webhook"} {
		tg, err := ForName(name)
		if err != nil {
			t.Fatalf("ForName(%q) error: %v", name, err)
		}
		if tg.Name() != want {
			t.Errorf("ForName(%q).Name() = %q, want %q", name, tg.Name(), want)
		}
	}
	if _, err := ForName("grpc"); err == nil {
		t.Fatal("ForName(unknown) = nil error, want error")
	} else if !strings.Contains(err.Error(), "grpc") {
		t.Errorf("unknown-target error %q should name the bad target", err)
	}
}

// ---------------------------------------------------------------------------
// extractToolCalls (shared helper)
// ---------------------------------------------------------------------------

func TestExtractToolCalls(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []ToolCallInfo
	}{
		{"tool_name + object args", `[{"tool_name":"search","arguments":{"q":"x"}}]`,
			[]ToolCallInfo{{Name: "search", Arguments: `{"q":"x"}`}}},
		{"name + string args", `[{"name":"calc","arguments":"1+1"}]`,
			[]ToolCallInfo{{Name: "calc", Arguments: "1+1"}}},
		{"action_name + args", `[{"action_name":"web","args":"query text"}]`,
			[]ToolCallInfo{{Name: "web", Arguments: "query text"}}},
		{"tool_name wins over name", `[{"tool_name":"a","name":"b"}]`,
			[]ToolCallInfo{{Name: "a"}}},
		{"array args stringified", `[{"name":"n","args":["a","b"]}]`,
			[]ToolCallInfo{{Name: "n", Arguments: `["a","b"]`}}},
		{"null args skipped, falls to args", `[{"name":"n","arguments":null,"args":"fb"}]`,
			[]ToolCallInfo{{Name: "n", Arguments: "fb"}}},
		{"multiple items", `[{"name":"one"},{"tool_name":"two","arguments":"y"}]`,
			[]ToolCallInfo{{Name: "one"}, {Name: "two", Arguments: "y"}}},
		{"empty array", `[]`, nil},
		{"not an array", `{"x":1}`, nil},
		{"empty input", ``, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractToolCalls(json.RawMessage(c.raw))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("extractToolCalls(%s) = %#v, want %#v", c.raw, got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// v1 target
// ---------------------------------------------------------------------------

func TestV1Target(t *testing.T) {
	var gotBody []byte
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("v1: unexpected path %q", r.URL.Path)
		}
		gotBody, _ = io.ReadAll(r.Body)
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","content":"The answer is 42.","reasoning_content":"thinking hard"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18},
			"docsgpt":{"conversation_id":"conv-xyz","sources":[{"title":"Doc A"},{"title":"Doc B"}],"tool_calls":[{"action_name":"web_search","args":"docsgpt cli"}]}
		}`)
	}))
	defer srv.Close()

	res, err := v1Target{}.Run(context.Background(), Request{
		Question: "What is the answer?",
		BaseURL:  srv.URL,
		APIKey:   "sk-test",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("v1 Run: %v", err)
	}

	if res.Answer != "The answer is 42." {
		t.Errorf("Answer = %q", res.Answer)
	}
	if res.Thought != "thinking hard" {
		t.Errorf("Thought = %q", res.Thought)
	}
	if res.ConversationID != "conv-xyz" {
		t.Errorf("ConversationID = %q", res.ConversationID)
	}
	if len(res.Sources) != 2 || res.Sources[0]["title"] != "Doc A" {
		t.Errorf("Sources = %#v", res.Sources)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 18 || res.Usage.PromptTokens != 11 {
		t.Errorf("Usage = %#v", res.Usage)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "web_search" || res.ToolCalls[0].Arguments != "docsgpt cli" {
		t.Errorf("ToolCalls = %#v", res.ToolCalls)
	}
	if res.Latency <= 0 {
		t.Errorf("Latency = %v, want > 0", res.Latency)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization header = %q", gotAuth)
	}

	// Verify request body: model, stream:false, message, and no docsgpt ext.
	var req struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if req.Model != "docsgpt" || req.Stream {
		t.Errorf("request model=%q stream=%v", req.Model, req.Stream)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content != "What is the answer?" {
		t.Errorf("request messages = %#v", req.Messages)
	}
	if m := map[string]json.RawMessage{}; json.Unmarshal(gotBody, &m) == nil {
		if _, ok := m["docsgpt"]; ok {
			t.Errorf("request should omit docsgpt when no attachments; body=%s", gotBody)
		}
	}
}

func TestV1TargetAttachments(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	_, err := v1Target{}.Run(context.Background(), Request{
		Question:      "summarize the files",
		AttachmentIDs: []string{"att-1", "att-2"},
		BaseURL:       srv.URL,
		APIKey:        "sk",
	})
	if err != nil {
		t.Fatalf("v1 Run: %v", err)
	}
	var req struct {
		DocsGPT struct {
			Attachments []string `json:"attachments"`
		} `json:"docsgpt"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(req.DocsGPT.Attachments, []string{"att-1", "att-2"}) {
		t.Errorf("docsgpt.attachments = %#v, body=%s", req.DocsGPT.Attachments, gotBody)
	}
}

func TestV1TargetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"invalid api key"}`)
	}))
	defer srv.Close()

	_, err := v1Target{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "bad"})
	if err == nil {
		t.Fatal("expected error on 401")
	}
	for _, want := range []string{"v1 target", "401", "invalid api key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
}

// ---------------------------------------------------------------------------
// stream target
// ---------------------------------------------------------------------------

func TestStreamTarget(t *testing.T) {
	var gotBody []byte
	body := strings.Join([]string{
		"id: 1",
		`data: {"type":"id","id":"conv-stream"}`, "",
		`data: {"type":"thought","thought":"let me think"}`, "",
		`data: {"type":"answer","answer":"Hello "}`, "",
		`data: {"type":"answer","answer":"world"}`, "",
		`data: {"type":"source","source":[{"title":"old"}]}`, "",
		`data: {"type":"source","source":[{"title":"A"},{"title":"B"}]}`, "",
		`data: {"type":"tool_calls","tool_calls":[{"tool_name":"search","arguments":{"q":"x"}}]}`, "",
		`data: {"type":"notice","notice":"ignore me"}`, "",
		`data: {"type":"end"}`, "",
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			t.Errorf("stream: unexpected path %q", r.URL.Path)
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}))
	defer srv.Close()

	res, err := streamTarget{}.Run(context.Background(), Request{
		Question:      "hi",
		AttachmentIDs: []string{"a1"},
		BaseURL:       srv.URL,
		APIKey:        "key-9",
	})
	if err != nil {
		t.Fatalf("stream Run: %v", err)
	}
	if res.Answer != "Hello world" {
		t.Errorf("Answer = %q", res.Answer)
	}
	if res.Thought != "let me think" {
		t.Errorf("Thought = %q", res.Thought)
	}
	if res.ConversationID != "conv-stream" {
		t.Errorf("ConversationID = %q", res.ConversationID)
	}
	if len(res.Sources) != 2 || res.Sources[0]["title"] != "A" {
		t.Errorf("Sources = %#v (should be replaced, not appended)", res.Sources)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "search" || res.ToolCalls[0].Arguments != `{"q":"x"}` {
		t.Errorf("ToolCalls = %#v", res.ToolCalls)
	}

	var req struct {
		Question    string   `json:"question"`
		APIKey      string   `json:"api_key"`
		Attachments []string `json:"attachments"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.Question != "hi" || req.APIKey != "key-9" || !reflect.DeepEqual(req.Attachments, []string{"a1"}) {
		t.Errorf("request = %+v (body=%s)", req, gotBody)
	}
}

func TestStreamTargetStructuredAnswer(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"answer","answer":"partial that gets replaced"}`, "",
		`data: {"type":"structured_answer","answer":{"result":"done","score":9}}`, "",
		`data: {"type":"end"}`, "",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	res, err := streamTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("stream Run: %v", err)
	}
	if res.Answer != `{"result":"done","score":9}` {
		t.Errorf("structured Answer = %q", res.Answer)
	}
}

func TestStreamTargetStructuredString(t *testing.T) {
	body := `data: {"type":"structured_answer","answer":"just a string"}` + "\n\n" + `data: {"type":"end"}` + "\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()
	res, err := streamTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("stream Run: %v", err)
	}
	if res.Answer != "just a string" {
		t.Errorf("Answer = %q", res.Answer)
	}
}

func TestStreamTargetError(t *testing.T) {
	body := `data: {"type":"answer","answer":"nope"}` + "\n\n" + `data: {"type":"error","error":"boom happened"}` + "\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()
	_, err := streamTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	if err == nil || !strings.Contains(err.Error(), "boom happened") {
		t.Fatalf("expected error event to fail with message, got %v", err)
	}
}

func TestStreamTargetLongFrame(t *testing.T) {
	// A single frame larger than bufio.Scanner's default 64 KiB token limit;
	// proves the 1 MiB buffer is in effect.
	big := strings.Repeat("z", 100_000)
	body := `data: {"type":"answer","answer":"` + big + `"}` + "\n\n" + `data: {"type":"end"}` + "\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()
	res, err := streamTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("stream Run (long frame): %v", err)
	}
	if len(res.Answer) != 100_000 {
		t.Errorf("Answer length = %d, want 100000", len(res.Answer))
	}
}

func TestStreamTargetEOFWithoutEnd(t *testing.T) {
	// Stream closes after an answer but sends no explicit end frame.
	body := `data: {"type":"answer","answer":"only answer"}` + "\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()
	res, err := streamTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("missing end event should be tolerated: %v", err)
	}
	if res.Answer != "only answer" {
		t.Errorf("Answer = %q", res.Answer)
	}
}

func TestStreamTargetEmptyStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// no frames at all
	}))
	defer srv.Close()
	_, err := streamTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	if err == nil {
		t.Fatal("empty stream with no answer/end should error")
	}
}

func TestStreamTargetHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "upstream down")
	}))
	defer srv.Close()
	_, err := streamTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "stream target") {
		t.Fatalf("expected 502 stream error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// webhook target
// ---------------------------------------------------------------------------

const webhookSuccess = `{"status":"SUCCESS","result":{"status":"success","result":{"answer":"async answer","sources":[{"t":"s1"}],"tool_calls":[{"name":"lookup","arguments":"q"}],"thought":"pondered","conversation_id":"c-1"}}}`

func TestWebhookTarget(t *testing.T) {
	var polls atomic.Int32
	var webhookBody []byte
	mux := http.NewServeMux()
	mux.HandleFunc("/api/webhooks/agents/tok", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("webhook method = %s", r.Method)
		}
		webhookBody, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"success":true,"task_id":"task-1"}`)
	})
	mux.HandleFunc("/api/task_status", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("task_id"); got != "task-1" {
			t.Errorf("task_status task_id = %q", got)
		}
		switch polls.Add(1) {
		case 1:
			io.WriteString(w, `{"status":"PENDING"}`)
		case 2:
			io.WriteString(w, `{"status":"PROGRESS"}`)
		default:
			io.WriteString(w, webhookSuccess)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := webhookTarget{}.Run(context.Background(), Request{
		Question:     "async question",
		BaseURL:      srv.URL,
		WebhookURL:   srv.URL + "/api/webhooks/agents/tok",
		Timeout:      3 * time.Second,
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("webhook Run: %v", err)
	}
	if res.Answer != "async answer" {
		t.Errorf("Answer = %q", res.Answer)
	}
	if res.Thought != "pondered" {
		t.Errorf("Thought = %q", res.Thought)
	}
	if res.ConversationID != "c-1" {
		t.Errorf("ConversationID = %q", res.ConversationID)
	}
	if len(res.Sources) != 1 || res.Sources[0]["t"] != "s1" {
		t.Errorf("Sources = %#v", res.Sources)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "lookup" || res.ToolCalls[0].Arguments != "q" {
		t.Errorf("ToolCalls = %#v", res.ToolCalls)
	}
	if res.Latency <= 0 {
		t.Errorf("Latency = %v", res.Latency)
	}
	if polls.Load() < 3 {
		t.Errorf("expected >=3 polls, got %d", polls.Load())
	}
	var wb map[string]string
	if err := json.Unmarshal(webhookBody, &wb); err != nil {
		t.Fatalf("unmarshal webhook body: %v", err)
	}
	if wb["question"] != "async question" || len(wb) != 1 {
		t.Errorf("webhook body = %v", wb)
	}
}

func TestWebhookTargetFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wh", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"task_id":"t2"}`)
	})
	mux.HandleFunc("/api/task_status", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"FAILURE","result":"kaboom in the worker"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := webhookTarget{}.Run(context.Background(), Request{
		Question: "q", BaseURL: srv.URL, WebhookURL: srv.URL + "/wh",
		Timeout: 2 * time.Second, PollInterval: 5 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "failed") || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("expected FAILURE error with payload, got %v", err)
	}
}

func TestWebhookTargetTransient503ThenSuccess(t *testing.T) {
	// 503 from task_status is a false negative under busy solo Celery pools;
	// the poller must ride it out instead of failing the run.
	var polls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/wh", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"task_id":"t3"}`)
	})
	mux.HandleFunc("/api/task_status", func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, "no workers")
			return
		}
		io.WriteString(w, `{"status":"SUCCESS","result":{"status":"success","result":{"answer":"rode it out"}}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := webhookTarget{}.Run(context.Background(), Request{
		Question: "q", BaseURL: srv.URL, WebhookURL: srv.URL + "/wh",
		Timeout: 2 * time.Second, PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("transient 503 should be tolerated, got %v", err)
	}
	if res.Answer != "rode it out" {
		t.Errorf("answer = %q", res.Answer)
	}
	if polls.Load() < 3 {
		t.Errorf("expected >=3 polls, got %d", polls.Load())
	}
}

func TestWebhookTargetPersistent503TimesOut(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wh", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"task_id":"t3"}`)
	})
	mux.HandleFunc("/api/task_status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := webhookTarget{}.Run(context.Background(), Request{
		Question: "q", BaseURL: srv.URL, WebhookURL: srv.URL + "/wh",
		Timeout: 100 * time.Millisecond, PollInterval: 5 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("expected deadline error after persistent 503, got %v", err)
	}
}

func TestWebhookTargetRedactsTokenInErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/webhooks/agents/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	const token = "sekrit-token-xyz"
	_, err := webhookTarget{}.Run(context.Background(), Request{
		Question: "q", BaseURL: srv.URL,
		WebhookURL:   srv.URL + "/api/webhooks/agents/" + token,
		Timeout:      time.Second,
		PollInterval: 5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("error leaks webhook token: %v", err)
	}
	if !strings.Contains(err.Error(), "/api/webhooks/agents/") {
		t.Errorf("error should keep the redacted URL context: %v", err)
	}
}

func TestWebhookTargetInnerAgentFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wh", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"task_id":"t4"}`)
	})
	mux.HandleFunc("/api/task_status", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"SUCCESS","result":{"status":"error","error":"agent blew up"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := webhookTarget{}.Run(context.Background(), Request{
		Question: "q", BaseURL: srv.URL, WebhookURL: srv.URL + "/wh",
		Timeout: time.Second, PollInterval: 5 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), `agent run reported "error"`) {
		t.Fatalf("expected inner agent failure error, got %v", err)
	}
}

func TestRedactWebhookURL(t *testing.T) {
	cases := map[string]string{
		"http://h:1/api/webhooks/agents/tok123": "http://h:1/api/webhooks/agents/...",
		"http://h/tok?x=1":                      "http://h/...",
		"://bad":                                "<unparseable webhook URL>",
	}
	for in, want := range cases {
		if got := redactWebhookURL(in); got != want {
			t.Errorf("redactWebhookURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWebhookTargetTimeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wh", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"task_id":"t4"}`)
	})
	mux.HandleFunc("/api/task_status", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"PENDING"}`) // never completes
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := webhookTarget{}.Run(context.Background(), Request{
		Question: "q", BaseURL: srv.URL, WebhookURL: srv.URL + "/wh",
		Timeout: 40 * time.Millisecond, PollInterval: 5 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestWebhookTargetMissingTaskID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true}`)
	}))
	defer srv.Close()
	_, err := webhookTarget{}.Run(context.Background(), Request{
		Question: "q", BaseURL: srv.URL, WebhookURL: srv.URL, Timeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "task_id") {
		t.Fatalf("expected missing task_id error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// attachments
// ---------------------------------------------------------------------------

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return p
}

func TestUploadAttachmentsDirect(t *testing.T) {
	var sawAPIKey string
	var sawContents []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/store_attachment" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		sawAPIKey = r.FormValue("api_key")
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		b, _ := io.ReadAll(f)
		f.Close()
		sawContents = append(sawContents, string(b))
		// echo an id derived from the uploaded filename
		id := "att-" + strings.TrimSuffix(hdr.Filename, ".txt")
		io.WriteString(w, `{"success":true,"attachment_id":"`+id+`"}`)
	}))
	defer srv.Close()

	pa := writeTempFile(t, "a.txt", "alpha")
	pb := writeTempFile(t, "b.txt", "bravo")

	ids, err := UploadAttachments(context.Background(), srv.URL, "sk-key", []string{pa, pb}, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("UploadAttachments: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"att-a", "att-b"}) {
		t.Errorf("ids = %#v, want [att-a att-b] (order matters)", ids)
	}
	if sawAPIKey != "sk-key" {
		t.Errorf("api_key form field = %q", sawAPIKey)
	}
	if !reflect.DeepEqual(sawContents, []string{"alpha", "bravo"}) {
		t.Errorf("uploaded contents = %#v", sawContents)
	}
}

func TestUploadAttachmentsTaskPoll(t *testing.T) {
	var polls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/store_attachment", func(w http.ResponseWriter, r *http.Request) {
		// multi-task variant with a null attachment_id -> caller must poll
		io.WriteString(w, `{"success":true,"tasks":[{"task_id":"tk-9","filename":"f.txt","attachment_id":null}]}`)
	})
	mux.HandleFunc("/api/task_status", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("task_id"); got != "tk-9" {
			t.Errorf("task_status task_id = %q", got)
		}
		if polls.Add(1) < 2 {
			io.WriteString(w, `{"status":"STARTED"}`)
			return
		}
		io.WriteString(w, `{"status":"SUCCESS","result":{"attachment_id":"att-polled"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := writeTempFile(t, "f.txt", "data")
	ids, err := UploadAttachments(context.Background(), srv.URL, "k", []string{p}, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("UploadAttachments: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"att-polled"}) {
		t.Errorf("ids = %#v, want [att-polled]", ids)
	}
	if polls.Load() < 2 {
		t.Errorf("expected polling, got %d polls", polls.Load())
	}
}

func TestUploadAttachmentsTopLevelTask(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/store_attachment", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"task_id":"tk-top","attachment_id":""}`)
	})
	mux.HandleFunc("/api/task_status", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"SUCCESS","result":{"attachment_id":"att-top"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := writeTempFile(t, "g.txt", "x")
	ids, err := UploadAttachments(context.Background(), srv.URL, "k", []string{p}, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("UploadAttachments: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"att-top"}) {
		t.Errorf("ids = %#v, want [att-top]", ids)
	}
}

func TestUploadAttachmentsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	}))
	defer srv.Close()
	p := writeTempFile(t, "h.txt", "x")
	_, err := UploadAttachments(context.Background(), srv.URL, "k", []string{p}, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

func TestUploadAttachmentsOpenError(t *testing.T) {
	_, err := UploadAttachments(context.Background(), "http://example.invalid", "k",
		[]string{"/no/such/file/really.txt"}, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "open file") {
		t.Fatalf("expected open error, got %v", err)
	}
}

func TestUploadAttachmentsWaitsForTaskDespiteDirectID(t *testing.T) {
	// The server may hand out attachment_id before the content-extraction task
	// has run; the uploader must still wait for the task to finish.
	var polls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/store_attachment", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"attachment_id":"att-race","task_id":"tk-race"}`)
	})
	mux.HandleFunc("/api/task_status", func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) == 1 {
			io.WriteString(w, `{"status":"PENDING","result":null}`)
			return
		}
		io.WriteString(w, `{"status":"SUCCESS","result":{"attachment_id":"att-race"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := writeTempFile(t, "race.txt", "content")
	ids, err := UploadAttachments(context.Background(), srv.URL, "k", []string{p}, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("UploadAttachments: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"att-race"}) {
		t.Errorf("ids = %#v", ids)
	}
	if polls.Load() < 2 {
		t.Errorf("expected task polling despite direct id, got %d polls", polls.Load())
	}
}

func TestUploadAttachmentsTaskSuccessWithoutIDFallsBack(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/store_attachment", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"attachment_id":"att-only","task_id":"tk-noid"}`)
	})
	mux.HandleFunc("/api/task_status", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"SUCCESS","result":{}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := writeTempFile(t, "noid.txt", "content")
	ids, err := UploadAttachments(context.Background(), srv.URL, "k", []string{p}, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("UploadAttachments: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"att-only"}) {
		t.Errorf("ids = %#v, want direct id fallback", ids)
	}
}
