package target

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// --- v1 -------------------------------------------------------------------

func TestV1TargetModelHistoryAndHeaders(t *testing.T) {
	var gotBody []byte
	var gotUA, gotTag string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotUA = r.Header.Get("User-Agent")
		gotTag = r.Header.Get(BenchTagHeader)
		io.WriteString(w, `{"choices":[{"message":{"content":"Zephyr"}}]}`)
	}))
	defer srv.Close()

	res, err := v1Target{}.Run(context.Background(), Request{
		Question: "What is my project called?",
		Model:    "gpt-test",
		History:  []Exchange{{Question: "My project is Zephyr.", Answer: "Noted."}},
		BaseURL:  srv.URL, APIKey: "k", RunTag: "nightly",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != "Zephyr" {
		t.Errorf("Answer = %q", res.Answer)
	}
	if gjson.GetBytes(gotBody, "model").String() != "gpt-test" {
		t.Errorf("model not forwarded: %s", gotBody)
	}
	msgs := gjson.GetBytes(gotBody, "messages").Array()
	if len(msgs) != 3 || msgs[0].Get("role").String() != "user" || msgs[1].Get("role").String() != "assistant" ||
		msgs[1].Get("content").String() != "Noted." || msgs[2].Get("content").String() != "What is my project called?" {
		t.Errorf("history not replayed: %s", gotBody)
	}
	if gjson.GetBytes(gotBody, "stream").Bool() {
		t.Errorf("stream should be false by default: %s", gotBody)
	}
	if !strings.Contains(gotUA, "docsgpt-cli") || gotTag != "bench:nightly" {
		t.Errorf("headers: UA=%q tag=%q", gotUA, gotTag)
	}
}

func TestV1TargetInlineFiles(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "report.pdf")
	png := filepath.Join(dir, "chart.png")
	os.WriteFile(pdf, []byte("%PDF-1.4 fake"), 0o644)
	os.WriteFile(png, []byte{0x89, 'P', 'N', 'G'}, 0o644)

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	_, err := v1Target{}.Run(context.Background(), Request{
		Question:    "What is in the file?",
		InlineFiles: []InlineFile{{Name: "report.pdf", Path: pdf}, {Name: "chart.png", Path: png}},
		BaseURL:     srv.URL, APIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := gjson.GetBytes(gotBody, "messages.0.content").Array()
	if len(parts) != 3 {
		t.Fatalf("want 3 content parts, got %d: %s", len(parts), gotBody)
	}
	if parts[0].Get("type").String() != "text" || parts[0].Get("text").String() != "What is in the file?" {
		t.Errorf("text part: %s", parts[0].Raw)
	}
	if parts[1].Get("type").String() != "file" || parts[1].Get("file.filename").String() != "report.pdf" ||
		!strings.HasPrefix(parts[1].Get("file.file_data").String(), "data:application/pdf;base64,") {
		t.Errorf("file part: %s", parts[1].Raw)
	}
	if parts[2].Get("type").String() != "image_url" || !strings.HasPrefix(parts[2].Get("image_url.url").String(), "data:image/png;base64,") {
		t.Errorf("image part: %s", parts[2].Raw)
	}
	if gjson.GetBytes(gotBody, "docsgpt").Exists() {
		t.Errorf("no docsgpt.attachments expected in inline mode: %s", gotBody)
	}
}

func TestV1TargetInlineFileMissing(t *testing.T) {
	_, err := v1Target{}.Run(context.Background(), Request{
		Question: "q", InlineFiles: []InlineFile{{Path: "/nonexistent/x.pdf"}}, BaseURL: "http://127.0.0.1:1", APIKey: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "inline attachment") {
		t.Fatalf("want inline attachment error, got %v", err)
	}
}

func TestV1TargetStreamMode(t *testing.T) {
	var gotBody []byte
	chunks := []string{
		`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"thinking"},"finish_reason":null}]}`,
		`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
		`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":null}],"docsgpt":{"type":"source","sources":[{"title":"A"}]}}`,
		`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":null}],"docsgpt":{"type":"tool_call","data":{"status":"completed","call_id":"1","tool_name":"search","arguments":{"q":"x"}}}}`,
		`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":null}],"docsgpt":{"type":"tool_call","data":{"status":"completed","call_id":"1","tool_name":"search"}}}`,
		`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":null}],"docsgpt":{"type":"id","conversation_id":"conv-1"}}`,
		`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"c","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for i, c := range chunks {
			if i == 2 {
				time.Sleep(30 * time.Millisecond) // TTFT gap before the first content delta
			}
			io.WriteString(w, "data: "+c+"\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	res, err := v1Target{}.Run(context.Background(), Request{
		Question: "hi", Stream: true, BaseURL: srv.URL, APIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !gjson.GetBytes(gotBody, "stream").Bool() || !gjson.GetBytes(gotBody, "stream_options.include_usage").Bool() {
		t.Errorf("stream request body: %s", gotBody)
	}
	if res.Answer != "Hello world" || res.Thought != "thinking" {
		t.Errorf("Answer=%q Thought=%q", res.Answer, res.Thought)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v", res.Usage)
	}
	if len(res.Sources) != 1 || res.ConversationID != "conv-1" {
		t.Errorf("Sources=%v ConversationID=%q", res.Sources, res.ConversationID)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "search" || res.ToolCalls[0].Arguments != `{"q":"x"}` {
		t.Errorf("ToolCalls = %+v (should dedupe by call_id)", res.ToolCalls)
	}
	if res.FirstOutput < 25*time.Millisecond || res.FirstOutput > res.Latency {
		t.Errorf("FirstOutput = %v (latency %v)", res.FirstOutput, res.Latency)
	}
	if !res.EndFrame {
		t.Errorf("EndFrame should be set by [DONE]")
	}
}

func TestV1TargetStreamErrorChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"error\":{\"message\":\"model exploded\",\"type\":\"server_error\"}}\n\n")
	}))
	defer srv.Close()
	_, err := v1Target{}.Run(context.Background(), Request{Question: "hi", Stream: true, BaseURL: srv.URL, APIKey: "k"})
	var se *ServerError
	if !errors.As(err, &se) || se.Message != "model exploded" || se.Status != 200 {
		t.Fatalf("want ServerError with message, got %#v (%v)", se, err)
	}
}

func TestV1TargetStreamMalformedChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "data: {not json\n\n")
	}))
	defer srv.Close()
	_, err := v1Target{}.Run(context.Background(), Request{Question: "hi", Stream: true, BaseURL: srv.URL, APIKey: "k"})
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("want malformed chunk error, got %v", err)
	}
	var se *ServerError
	if errors.As(err, &se) {
		t.Errorf("malformed chunks are transport-level errors, not ServerError")
	}
}

func TestV1TargetHTTPErrorIsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"message":"Invalid API key","type":"auth_error"}}`)
	}))
	defer srv.Close()
	_, err := v1Target{}.Run(context.Background(), Request{Question: "hi", BaseURL: srv.URL, APIKey: "bad"})
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("want ServerError, got %T %v", err, err)
	}
	if se.Status != 401 || se.Message != "Invalid API key" || !strings.Contains(err.Error(), "returned 401") {
		t.Errorf("ServerError = %+v (%v)", se, err)
	}
}

// --- stream ---------------------------------------------------------------

func TestStreamTargetModelConversationTTFTFrames(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			io.WriteString(w, "data: "+s+"\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
		write(`{"type":"message_id","message_id":"m1"}`)
		write(`{"type":"thought","thought":"hmm"}`)
		time.Sleep(30 * time.Millisecond)
		write(`{"type":"answer","answer":"Hi"}`)
		write(`{"type":"source","source":[{"title":"A"}]}`)
		write(`{"type":"id","id":"conv-9"}`)
		write(`{"type":"end"}`)
	}))
	defer srv.Close()

	res, err := streamTarget{}.Run(context.Background(), Request{
		Question: "hi", Model: "gpt-x", ConversationID: "conv-prev", BaseURL: srv.URL, APIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(gotBody, "model_id").String() != "gpt-x" || gjson.GetBytes(gotBody, "conversation_id").String() != "conv-prev" {
		t.Errorf("request body missing model_id/conversation_id: %s", gotBody)
	}
	if res.FirstOutput < 25*time.Millisecond || res.FirstOutput > res.Latency {
		t.Errorf("FirstOutput = %v (latency %v)", res.FirstOutput, res.Latency)
	}
	wantFrames := []string{"message_id", "thought", "answer", "source", "id", "end"}
	if strings.Join(res.Frames, ",") != strings.Join(wantFrames, ",") {
		t.Errorf("Frames = %v", res.Frames)
	}
	if !res.EndFrame || res.ErrorFrame || res.ConversationID != "conv-9" {
		t.Errorf("EndFrame=%v ErrorFrame=%v conv=%q", res.EndFrame, res.ErrorFrame, res.ConversationID)
	}
}

func TestStreamTargetOmitsEmptyModelAndConversation(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		io.WriteString(w, "data: {\"type\":\"answer\",\"answer\":\"x\"}\n\ndata: {\"type\":\"end\"}\n\n")
	}))
	defer srv.Close()
	if _, err := (streamTarget{}).Run(context.Background(), Request{Question: "hi", BaseURL: srv.URL, APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	json.Unmarshal(gotBody, &m)
	if _, ok := m["model_id"]; ok {
		t.Errorf("model_id must be omitted when unset: %s", gotBody)
	}
	if _, ok := m["conversation_id"]; ok {
		t.Errorf("conversation_id must be omitted when unset: %s", gotBody)
	}
}

func TestStreamTargetErrorFrameIsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(400)
		io.WriteString(w, "data: {\"type\":\"error\",\"error\":\"Malformed request body\"}\n\n")
	}))
	defer srv.Close()
	_, err := streamTarget{}.Run(context.Background(), Request{Question: "hi", BaseURL: srv.URL, APIKey: "k"})
	var se *ServerError
	if !errors.As(err, &se) || se.Status != 400 || se.Message != "Malformed request body" {
		t.Fatalf("want ServerError{400, Malformed...}, got %#v (%v)", se, err)
	}
}

func TestStreamTargetMidStreamErrorFrame(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "data: {\"type\":\"answer\",\"answer\":\"partial\"}\n\ndata: {\"type\":\"error\",\"error\":\"content_filter\"}\n\n")
	}))
	defer srv.Close()
	_, err := streamTarget{}.Run(context.Background(), Request{Question: "hi", BaseURL: srv.URL, APIKey: "k"})
	var se *ServerError
	if !errors.As(err, &se) || se.Status != 200 || se.Message != "content_filter" {
		t.Fatalf("want ServerError{200, content_filter}, got %#v (%v)", se, err)
	}
	if strings.Contains(err.Error(), "returned 200") {
		t.Errorf("a 200 stream error should not read as an HTTP failure: %v", err)
	}
}

// --- answer ---------------------------------------------------------------

func TestAnswerTarget(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/answer" {
			t.Errorf("path %q", r.URL.Path)
		}
		gotBody, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"conversation_id":"c1","answer":"ANSWER-OK","sources":[{"title":"A"}],"tool_calls":[{"tool_name":"t","arguments":"a"}],"thought":"th"}`)
	}))
	defer srv.Close()

	res, err := answerTarget{}.Run(context.Background(), Request{
		Question: "Reply", Model: "m1", ConversationID: "c0", BaseURL: srv.URL, APIKey: "k", RunTag: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != "ANSWER-OK" || res.ConversationID != "c1" || res.Thought != "th" || len(res.Sources) != 1 || len(res.ToolCalls) != 1 {
		t.Errorf("result = %+v", res)
	}
	if res.FirstOutput != 0 || res.Latency <= 0 {
		t.Errorf("answer target cannot observe TTFT: %+v", res)
	}
	if gjson.GetBytes(gotBody, "api_key").String() != "k" || gjson.GetBytes(gotBody, "model_id").String() != "m1" ||
		gjson.GetBytes(gotBody, "conversation_id").String() != "c0" || gjson.GetBytes(gotBody, "question").String() != "Reply" {
		t.Errorf("request body: %s", gotBody)
	}
}

func TestAnswerTargetStructuredAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"conversation_id":"c1","answer":{"a":1},"structured":true}`)
	}))
	defer srv.Close()
	res, err := answerTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != `{"a":1}` {
		t.Errorf("Answer = %q", res.Answer)
	}
}

func TestAnswerTargetErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"http 400", 400, `{"error":"Malformed request body"}`, "Malformed request body"},
		{"http 500", 500, `{"error":"An error occurred processing your request"}`, "An error occurred"},
		{"200 with error", 200, `{"error":"stream failed"}`, "stream failed"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.body)
		}))
		_, err := answerTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
		srv.Close()
		var se *ServerError
		if !errors.As(err, &se) || se.Status != tc.status || !strings.Contains(se.Message, tc.want) {
			t.Errorf("%s: got %#v (%v)", tc.name, se, err)
		}
	}
}

func TestAnswerTargetNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>oops</html>")
	}))
	defer srv.Close()
	_, err := answerTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	var se *ServerError
	if err == nil || errors.As(err, &se) {
		t.Fatalf("non-JSON 200 must be a plain error, got %v", err)
	}
}

// --- webhook typed errors ---------------------------------------------------

func TestWebhookTargetErrorsAreServerErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wh", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"success":false,"message":"Webhook not found"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	_, err := webhookTarget{}.Run(context.Background(), Request{
		Question: "q", BaseURL: srv.URL, WebhookURL: srv.URL + "/wh", Timeout: time.Second, PollInterval: 5 * time.Millisecond,
	})
	var se *ServerError
	if !errors.As(err, &se) || se.Status != 404 || se.Message != "Webhook not found" {
		t.Fatalf("want ServerError{404}, got %#v (%v)", se, err)
	}
}

// --- helpers ----------------------------------------------------------------

func TestForNameAnswer(t *testing.T) {
	tg, err := ForName("answer")
	if err != nil || tg.Name() != "answer" {
		t.Fatalf("ForName(answer) = %v, %v", tg, err)
	}
	if _, err := ForName("nope"); err == nil || !strings.Contains(err.Error(), "answer") {
		t.Errorf("unknown target error should list answer: %v", err)
	}
}

func TestErrorMessage(t *testing.T) {
	cases := map[string]string{
		`{"error":{"message":"Invalid API key","type":"auth_error"}}`: "Invalid API key",
		`{"error":"Malformed request body"}`:                          "Malformed request body",
		`{"success":false,"message":"Exceeding usage limit"}`:         "Exceeding usage limit",
		"data: {\"type\":\"error\",\"error\":\"Unauthorized\"}\n\n":   "Unauthorized",
		`plain text body`:       "plain text body",
		`{"error":{"code":42}}`: `{"error":{"code":42}}`,
	}
	for in, want := range cases {
		if got := errorMessage([]byte(in)); got != want {
			t.Errorf("errorMessage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServerErrorString(t *testing.T) {
	e := &ServerError{Status: 422, Message: "bad", Where: "v1 target: /x"}
	if e.Error() != "v1 target: /x returned 422: bad" {
		t.Errorf("Error() = %q", e.Error())
	}
	e = &ServerError{Message: "boom", Where: "task t1 failed"}
	if e.Error() != "task t1 failed: boom" {
		t.Errorf("Error() = %q", e.Error())
	}
}
