package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/session"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// fakeAgent answers /v1/chat/completions, continuing the conversation the
// request names or starting c1, c2, …, and records /api/feedback.
type fakeAgent struct {
	mu       sync.Mutex
	convs    int
	fail     bool                  // the next answer is an error
	requests []docsgpt.ChatRequest // the chat requests
	feedback []map[string]any      // the feedback bodies
}

func (f *fakeAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/api/feedback" {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.feedback = append(f.feedback, body)
		jsonReply(w, 200, `{"success":true}`)
		return
	}
	var req docsgpt.ChatRequest
	json.NewDecoder(r.Body).Decode(&req)
	f.requests = append(f.requests, req)
	if f.fail {
		f.fail = false
		jsonReply(w, 500, `{"error":{"message":"boom"}}`)
		return
	}
	conv := req.ConversationID
	if conv == "" {
		f.convs++
		conv = fmt.Sprintf("c%d", f.convs)
	}
	jsonReply(w, 200, fmt.Sprintf(`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"answer %d"}}],
		"docsgpt":{"conversation_id":%q},"usage":{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010}}`, len(f.requests), conv))
}

func testChat(t *testing.T) (*chatSession, *fakeAgent) {
	t.Helper()
	isolateConfig(t)
	oldCtx, oldStream := globalNoContext, globalNoStream
	globalNoContext, globalNoStream = true, true
	t.Cleanup(func() { globalNoContext, globalNoStream = oldCtx, oldStream })
	agent := &fakeAgent{}
	srv := httptest.NewServer(agent)
	t.Cleanup(srv.Close)
	scr := ui.NewScreen(ui.ScreenOptions{Headless: true})
	s := &chatSession{
		cfg: config.Config{Keys: map[string]string{"k": "key-k"}}, keyName: "k", baseURL: srv.URL,
		client: docsgpt.NewClient(srv.URL, "key-k"), tools: &tools.Session{UI: &screenTools{scr: scr}},
		sess: session.New(t.TempDir(), srv.URL, "k"), scr: scr, ctx: context.Background(),
	}
	return s, agent
}

// lastFeedback returns the latest feedback body as conversation/index.
func (f *fakeAgent) lastFeedback(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.feedback) == 0 {
		t.Fatal("no feedback sent")
	}
	b := f.feedback[len(f.feedback)-1]
	if b["api_key"] != "key-k" {
		t.Errorf("feedback key %v", b["api_key"])
	}
	return fmt.Sprintf("%v %v/%v", b["feedback"], b["conversation_id"], b["question_index"])
}

// TestRateQuestionIndex: /good and /bad rate the last answer by its index
// in its server conversation, counting again in a conversation started
// over (after an error, an edit), and on in a resumed one.
func TestRateQuestionIndex(t *testing.T) {
	s, agent := testChat(t)
	s.rateGood("")
	if len(agent.feedback) != 0 {
		t.Fatal("rated with nothing to rate")
	}
	s.send("a", "a")
	s.send("b", "b")
	s.rateGood("")
	if got := agent.lastFeedback(t); got != "like c1/1" {
		t.Fatalf("after two answers: %s", got)
	}

	agent.fail = true
	s.send("c", "c") // fails: the next turn starts a new conversation
	s.rateBad("")
	if got := agent.lastFeedback(t); got != "dislike c1/1" {
		t.Fatalf("after a failure, the last answer: %s", got)
	}
	s.retry("")
	if r := agent.requests[len(agent.requests)-1]; r.ConversationID != "" || len(r.Messages) != 5 || r.Messages[4].Content != "c" {
		t.Fatalf("retry after a failure: conversation %q, %d messages", r.ConversationID, len(r.Messages))
	}
	s.rateBad("")
	if got := agent.lastFeedback(t); got != "dislike c2/0" {
		t.Fatalf("after the retry: %s", got)
	}

	// Resumed: the same conversation goes on.
	resumed, _ := session.Load(s.sess.Path)
	s.resume(resumed)
	s.send("d", "d")
	s.rateGood("")
	if got := agent.lastFeedback(t); got != "like c2/1" {
		t.Fatalf("resumed: %s", got)
	}
	if s.totalUsage.PromptTokens != 4000 || s.lastUsage.CompletionTokens != 10 {
		t.Errorf("usage %+v / %+v", s.lastUsage, s.totalUsage)
	}
}

// TestForkReplaysHistory: editing message 2 goes on in a new session
// copying message 1, and a new server conversation replaying it; the old
// session stays whole.
func TestForkReplaysHistory(t *testing.T) {
	s, agent := testChat(t)
	s.send("one", "one")
	s.send("two", "two")
	s.send("three", "three")
	old := s.sess.Path

	s.fork(1, "── edited from message 2 ──")
	if s.sess.Path == old || len(s.history) != 2 || s.conversationID != "" {
		t.Fatalf("after the fork: %d messages, conversation %q", len(s.history), s.conversationID)
	}
	if s.totalUsage.PromptTokens != 1000 {
		t.Errorf("fork usage %+v", s.totalUsage)
	}
	s.send("two, better", "two, better")
	r := agent.requests[len(agent.requests)-1]
	if r.ConversationID != "" || len(r.Messages) != 3 || r.Messages[0].Content != "one" || r.Messages[2].Content != "two, better" {
		t.Fatalf("fork request: conversation %q, messages %+v", r.ConversationID, r.Messages)
	}
	s.rateGood("")
	if got := agent.lastFeedback(t); got != "like c2/0" {
		t.Fatalf("fork feedback: %s", got)
	}

	forked, _ := session.Load(s.sess.Path)
	if turns := forked.Turns(); len(turns) != 2 || turns[0].Question != "one" || turns[1].Question != "two, better" || forked.Parent != old {
		t.Fatalf("fork file: %+v", turns)
	}
	if orig, _ := session.Load(old); len(orig.Turns()) != 3 {
		t.Fatalf("the old session has %d turns", len(orig.Turns()))
	}
	// /retry asks again: the last question, in a fork of its own.
	before := len(agent.requests)
	s.retry("")
	if r := agent.requests[before]; len(r.Messages) != 3 || r.Messages[2].Content != "two, better" || len(agent.requests) != before+1 {
		t.Fatalf("retry: %+v", r.Messages)
	}
	if !strings.Contains(s.scr.Transcript(80), "message 2 again") {
		t.Error("no divider for the retry")
	}
}
