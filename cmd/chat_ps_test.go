package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/session"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// TestWakeSendsEvents: events start a turn of their own, saved as one the
// chat sent; past the cap they wait and go with the user's next message.
func TestWakeSendsEvents(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir())
	oldCtx, oldStream := globalNoContext, globalNoStream
	globalNoContext, globalNoStream = true, true
	t.Cleanup(func() { globalNoContext, globalNoStream = oldCtx, oldStream })

	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		jsonReply(w, 200, `{"conversation_id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"noted"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	scr := ui.NewScreen(ui.ScreenOptions{Headless: true})
	procs := tools.NewProcesses(nil)
	s := &chatSession{client: docsgpt.NewClient(srv.URL, "k"), tools: &tools.Session{UI: &screenTools{scr: scr}, Processes: procs},
		sess: session.New(t.TempDir(), srv.URL, "k"), scr: scr, ctx: context.Background(), procs: procs}

	procs.Unread([]tools.Event{{ID: "p1", Text: "p1 · npm test · exited with code 1 after 3.0s"}})
	s.wake()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "local_process_event") || !strings.Contains(bodies[0], "exited with code 1") {
		t.Fatalf("bodies = %q", bodies)
	}
	turns := s.sess.Turns()
	if len(turns) != 1 || !turns[0].Event || turns[0].Answer != "noted" {
		t.Fatalf("turns = %+v", turns)
	}
	if got := eventShown(turns[0].Question); got != "p1 · npm test · exited with code 1 after 3.0s" {
		t.Errorf("shown on resume: %q", got)
	}

	s.wake() // nothing waits: no turn
	s.autoTurns = maxAutoTurns
	procs.Unread([]tools.Event{{ID: "p2", Text: "p2 · monitor \"errors\": 1 line"}})
	s.wake()
	if len(bodies) != 1 || !s.held {
		t.Fatalf("a turn past the cap: %d requests, held %v", len(bodies), s.held)
	}
	s.send("what happened?", "what happened?")
	if len(bodies) != 2 || !strings.Contains(bodies[1], `monitor \"errors\"`) || !strings.Contains(bodies[1], "what happened?") {
		t.Fatalf("the user's message lacks the events: %q", bodies[1:])
	}
	if turns := s.sess.Turns(); turns[1].Event || turns[1].Question != "what happened?" {
		t.Errorf("user turn = %+v", turns[1])
	}
}
