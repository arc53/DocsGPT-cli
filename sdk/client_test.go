package docsgpt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"bad key"}`)
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "k").Send(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("Send() error = nil, want an error")
	}
	// The whole point of the typed error: callers can branch on the status
	// instead of matching on the message.
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Send() error = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Body, "bad key") {
		t.Errorf("Body = %q, want it to carry the server message", apiErr.Body)
	}
}

func TestSendSetsAuthAndDisablesStreaming(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"}}]}`)
	}))
	defer srv.Close()

	// A trailing slash on the base URL must not produce a double slash.
	resp, err := NewClient(srv.URL+"/", "secret").
		Send(context.Background(), ChatRequest{Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret")
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions (the trailing slash on the base URL must be trimmed)", gotPath)
	}
	if gotBody.Stream {
		t.Error("Send() sent stream:true; it must force streaming off")
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "hi" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestSendStreamAccumulates(t *testing.T) {
	// Two content chunks, a split tool call, a conversation id, and a
	// "stop" after "tool_calls" — which must not overwrite it.
	chunks := []string{
		`{"docsgpt":{"conversation_id":"conv-1"}}`,
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo","reasoning_content":"think"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","type":"function","function":{"name":"run","arguments":"{\"a\":"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"stop"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q, want text/event-stream", got)
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var seen int
	resp, err := NewClient(srv.URL, "k").SendStream(
		context.Background(), ChatRequest{}, func(Delta, string) { seen++ })
	if err != nil {
		t.Fatal(err)
	}
	if seen != 4 {
		t.Errorf("handler called %d times, want 4 (one per chunk with choices)", seen)
	}

	msg := resp.Choices[0].Message
	if msg.Content != "Hello" {
		t.Errorf("Content = %q, want %q", msg.Content, "Hello")
	}
	if msg.ReasoningContent != "think" {
		t.Errorf("ReasoningContent = %q, want %q", msg.ReasoningContent, "think")
	}
	if resp.DocsGPT.ConversationID != "conv-1" {
		t.Errorf("ConversationID = %q, want conv-1", resp.DocsGPT.ConversationID)
	}
	// The server keeps streaming after tool_calls; a later "stop" must not win,
	// or the caller would never run the tools.
	if got := resp.Choices[0].FinishReason; got != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", got)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	if got := msg.ToolCalls[0].Function.Arguments; got != `{"a":1}` {
		t.Errorf("Arguments = %q, want the two chunks joined", got)
	}
}

func TestSendStreamToleratesNilHandler(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	resp, err := NewClient(srv.URL, "k").SendStream(context.Background(), ChatRequest{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content != "x" {
		t.Errorf("Content = %q, want x", resp.Choices[0].Message.Content)
	}
}

func TestRunWithToolsFeedsResultsBack(t *testing.T) {
	var turns int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		turns++
		if turns == 1 {
			fmt.Fprint(w, `{"choices":[{"message":{"tool_calls":[{"id":"t1","function":{"name":"run","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"docsgpt":{"conversation_id":"c1"}}`)
			return
		}
		// The second turn must carry the tool result and the conversation id.
		if req.ConversationID != "c1" {
			t.Errorf("turn 2 ConversationID = %q, want c1", req.ConversationID)
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != "tool" || last.Content != "42" || last.ToolCallID != "t1" {
			t.Errorf("turn 2 last message = %+v, want the tool result for t1", last)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"done"}},{}],"docsgpt":{}}`)
	}))
	defer srv.Close()

	res, err := NewClient(srv.URL, "k").RunWithTools(
		context.Background(),
		[]Message{{Role: "user", Content: "go"}},
		RunOptions{OnToolCall: func(tc ToolCall) string { return "42" }},
	)
	if err != nil {
		t.Fatal(err)
	}
	history := res.Messages
	if turns != 2 {
		t.Errorf("made %d requests, want 2", turns)
	}
	// user, assistant(tool_calls), tool, assistant(final)
	if len(history) != 4 {
		t.Fatalf("history has %d messages, want 4: %+v", len(history), history)
	}
	if history[3].Content != "done" {
		t.Errorf("final message = %q, want done", history[3].Content)
	}
}

// sseServer replies to every request with the given data frames, recording
// each request body.
func sseServer(t *testing.T, frames ...string) (*httptest.Server, *[]ChatRequest) {
	t.Helper()
	var reqs []ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		reqs = append(reqs, req)
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

func TestSendStreamCollectsMetadata(t *testing.T) {
	big := strings.Repeat("x", 100*1024) // over bufio.Scanner's default line limit
	srv, reqs := sseServer(t,
		`{"model":"agent","choices":[{"delta":{},"finish_reason":null}],"docsgpt":{"type":"model","model":"gpt-x","provider":"p"}}`,
		`{"choices":[{"delta":{"content":"Hi"}}]}`,
		`{"choices":[{"delta":{}}],"docsgpt":{"type":"source","sources":[{"title":"Guide","source":"https://x/guide","text":"`+big+`"},"junk"]}}`,
		// Older servers send the extension chunks bare, without choices.
		`{"docsgpt":{"type":"id","conversation_id":"conv-9"}}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`,
	)

	resp, err := NewClient(srv.URL, "k").SendStream(context.Background(), ChatRequest{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := (*reqs)[0].StreamOptions; got == nil || !got.IncludeUsage {
		t.Errorf("stream_options = %+v, want include_usage", got)
	}
	if resp.Choices[0].Message.Content != "Hi" {
		t.Errorf("Content = %q, want Hi", resp.Choices[0].Message.Content)
	}
	if resp.DocsGPT.ConversationID != "conv-9" || resp.DocsGPT.Model != "gpt-x" || resp.Model != "agent" {
		t.Errorf("meta = %+v model %q, want conv-9 / gpt-x / agent", resp.DocsGPT, resp.Model)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 12 {
		t.Errorf("Usage = %+v, want total 12", resp.Usage)
	}
	sources := ParseSources(resp.DocsGPT.Sources)
	if len(sources) != 1 || sources[0].Title != "Guide" || len(sources[0].Text) != len(big) {
		t.Errorf("sources = %d entries, want the one well-formed entry", len(sources))
	}
}

func TestSendStreamErrorFrame(t *testing.T) {
	srv, _ := sseServer(t,
		`{"choices":[{"delta":{"content":"partial"}}]}`,
		`{"error":{"message":"quota exceeded","type":"server_error"}}`,
	)
	_, err := NewClient(srv.URL, "k").SendStream(context.Background(), ChatRequest{}, nil)
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("err = %v, want the server's error message", err)
	}
}

func TestRunWithToolsCarriesConversationAndMetadata(t *testing.T) {
	var reqs []ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		reqs = append(reqs, req)
		if len(reqs) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":null}],\"docsgpt\":{\"type\":\"source\",\"sources\":[{\"title\":\"A\"}]}}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"t1\",\"function\":{\"name\":\"run\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1,\"total_tokens\":6}}\n\n")
		} else {
			// The continuation reports no sources; the first round's stay.
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":null}],\"docsgpt\":{\"type\":\"source\",\"sources\":[]}}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"docsgpt\":{\"conversation_id\":\"c2\"}}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var content string
	res, err := NewClient(srv.URL, "k").RunWithTools(context.Background(),
		[]Message{{Role: "user", Content: "go"}},
		RunOptions{
			Stream:         true,
			ConversationID: "c1",
			OnDelta:        func(d Delta, _ string) { content += d.Content },
			OnToolCall:     func(ToolCall) string { return "ok" },
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 || reqs[0].ConversationID != "c1" || reqs[1].ConversationID != "c1" {
		t.Fatalf("requests = %d, conversation ids %q/%q; want 2 requests continuing c1",
			len(reqs), reqs[0].ConversationID, reqs[len(reqs)-1].ConversationID)
	}
	if res.ConversationID != "c2" {
		t.Errorf("ConversationID = %q, want the latest one the server named (c2)", res.ConversationID)
	}
	if len(res.Sources) != 1 || res.Sources[0].Title != "A" {
		t.Errorf("Sources = %+v, want the first round's source", res.Sources)
	}
	if res.Usage == nil || *res.Usage != (Usage{PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16}) {
		t.Errorf("Usage = %+v, want both rounds summed", res.Usage)
	}
	if content != "done" || res.Messages[len(res.Messages)-1].Content != "done" {
		t.Errorf("content = %q, final message %+v", content, res.Messages[len(res.Messages)-1])
	}
}
