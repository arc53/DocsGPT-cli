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

	history, err := NewClient(srv.URL, "k").RunWithTools(
		context.Background(),
		[]Message{{Role: "user", Content: "go"}},
		nil, false, nil,
		func(tc ToolCall) string { return "42" },
	)
	if err != nil {
		t.Fatal(err)
	}
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
