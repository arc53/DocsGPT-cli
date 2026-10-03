package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/session"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// TestExport: ~/ is the home directory, and an existing file is kept unless
// the user says to overwrite it (no one can here).
func TestExport(t *testing.T) {
	home := isolateConfig(t)
	t.Chdir(t.TempDir())
	s := &chatSession{sess: session.New("/w", "https://example.com", "k"), scr: ui.NewScreen(ui.ScreenOptions{Headless: true})}
	q, a := docsgpt.Message{Role: "user", Content: "q"}, docsgpt.Message{Role: "assistant", Content: "the answer"}
	s.sess.Record("https://example.com", "k", "c", session.Entry{Message: &q}, session.Entry{Message: &a})

	s.export("~/notes.md")
	if b, err := os.ReadFile(filepath.Join(home, "notes.md")); err != nil || !strings.Contains(string(b), "the answer") {
		t.Fatalf("~/notes.md: %q, %v", b, err)
	}
	if _, err := os.Stat("~"); err == nil {
		t.Fatal("exported into a directory named ~")
	}
	os.WriteFile("mine.md", []byte("keep"), 0o644)
	s.export("mine.md")
	if b, _ := os.ReadFile("mine.md"); string(b) != "keep" {
		t.Fatalf("overwrote an existing file: %q", b)
	}
}

// TestHandleDecidesOnWhatWasShown: a collapsed paste starting with ! or /
// is a message, never a shell or slash command.
func TestHandleDecidesOnWhatWasShown(t *testing.T) {
	isolateConfig(t)
	oldCtx, oldStream := globalNoContext, globalNoStream
	globalNoContext, globalNoStream = true, true
	t.Cleanup(func() { globalNoContext, globalNoStream = oldCtx, oldStream })

	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		jsonReply(w, 500, `{"error":"no"}`)
	}))
	defer srv.Close()
	scr := ui.NewScreen(ui.ScreenOptions{Headless: true})
	s := &chatSession{client: docsgpt.NewClient(srv.URL, "k"), tools: &tools.Session{UI: &screenTools{scr: scr}}, sess: session.New(t.TempDir(), srv.URL, "k"), scr: scr, ctx: context.Background()}

	marker := filepath.Join(t.TempDir(), "ran")
	pasted := "!touch " + marker + "\n" + strings.Repeat("more\n", 12)
	s.handle(ui.Message{Text: pasted, Shown: "[paste #1 +13 lines]"})
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a pasted !command ran")
	}
	s.handle(ui.Message{Text: "/new and then some\n" + strings.Repeat("x\n", 12), Shown: "[paste #1 +13 lines]"})
	if len(bodies) != 2 || !strings.Contains(bodies[0], "touch") || !strings.Contains(bodies[1], "/new and then some") {
		t.Fatalf("pastes were not sent as messages: %q", bodies)
	}

	s.handle(ui.Message{Text: "!touch " + marker, Shown: "!touch " + marker})
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("a typed !command did not run")
	}
	if s.handle(ui.Message{Text: "/nosuchcommand", Shown: "/nosuchcommand"}); len(bodies) != 2 {
		t.Fatal("a typed /command was sent")
	}
}
