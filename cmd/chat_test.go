package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/config"
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

// TestResetBeforeScreen: runChat resets the session before it makes the
// screen, which the tab status must not reach for.
func TestResetBeforeScreen(t *testing.T) {
	isolateConfig(t)
	s := &chatSession{baseURL: "https://example.com", keyName: "k"}
	s.reset()
	if s.sess == nil {
		t.Fatal("no session after reset")
	}
}

// tabChat is a chat on a headless screen whose terminal sequences go to a
// file, outside tmux and screen; statuses returns the tab statuses written
// since it was called last ("" for a clear).
func tabChat(t *testing.T) (s *chatSession, statuses func() []string) {
	t.Helper()
	isolateConfig(t)
	for _, env := range []string{"TMUX", "STY", "TERM"} {
		t.Setenv(env, "")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	scr := ui.NewScreen(ui.ScreenOptions{Headless: true, Output: f})
	s = &chatSession{cfg: config.DefaultConfig(), baseURL: "https://example.com", keyName: "k", scr: scr, ctx: context.Background(),
		sess: session.New(t.TempDir(), "https://example.com", "k")}
	s.tools = &tools.Session{UI: &screenTools{scr: scr, waiting: s.waiting}}
	status := regexp.MustCompile(`21337;status=([^;]*);`)
	return s, func() []string {
		t.Helper()
		b, _ := os.ReadFile(f.Name())
		f.Truncate(0)
		f.Seek(0, 0)
		var got []string
		for _, m := range status.FindAllStringSubmatch(string(b), -1) {
			got = append(got, m[1])
		}
		return got
	}
}

// TestTabStatusStartup: a chat starts by clearing a status an earlier one
// left (killed before it could), where statuses are sent.
func TestTabStatusStartup(t *testing.T) {
	for _, c := range []struct {
		mode, tmux string
		want       []string
	}{{"auto", "", []string{""}}, {"off", "", nil}, {"auto", "/tmp/tmux-1/default,1,0", nil}, {"on", "/tmp/tmux-1/default,1,0", []string{""}}} {
		s, statuses := tabChat(t)
		t.Setenv("TMUX", c.tmux)
		s.cfg.Settings.TabStatus = c.mode
		s.loop("")
		if got := statuses(); !slices.Equal(got, c.want) {
			t.Errorf("%s (tmux %q): %q, want %q", c.mode, c.tmux, got, c.want)
		}
	}
}

// TestTabStatusShell: a !cmd that fails shows failed, not done.
func TestTabStatusShell(t *testing.T) {
	s, statuses := tabChat(t)
	s.handle(ui.Message{Text: "!exit 3", Shown: "!exit 3"})
	if got := statuses(); !slices.Equal(got, []string{"working", "failed"}) {
		t.Errorf("failing !cmd: %q", got)
	}
	s.handle(ui.Message{Text: "!echo ok", Shown: "!echo ok"})
	if got := statuses(); !slices.Equal(got, []string{"working", "done"}) {
		t.Errorf("!cmd: %q", got)
	}
}

// TestTabStatusNotSent: a message whose files cannot go, put back into
// the input, shows failed rather than the status of the answer before.
func TestTabStatusNotSent(t *testing.T) {
	s, statuses := tabChat(t)
	t.Chdir(t.TempDir())
	os.WriteFile("empty.txt", nil, 0o644)
	s.tabStatus("done", tabDone)
	statuses()
	s.send("see @empty.txt", "see @empty.txt")
	if got := statuses(); !slices.Equal(got, []string{"failed"}) {
		t.Errorf("not sent: %q", got)
	}
}

// TestTabStatusSettings: changing tab_status shows the status again as
// the new mode has it: inside tmux, auto clears the status "on" sent, on
// sends it again, off clears it.
func TestTabStatusSettings(t *testing.T) {
	s, statuses := tabChat(t)
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	cfg := config.DefaultConfig()
	cfg.BaseURL = s.baseURL
	s.cfg.Settings.TabStatus = "on"
	s.tabStatus("working", tabWorking)
	statuses()
	for _, step := range []struct {
		mode string
		want []string
	}{{"auto", []string{""}}, {"on", []string{"working"}}, {"off", []string{""}}, {"auto", nil}, {"on", []string{"working"}}} {
		cfg.Settings.TabStatus = step.mode
		s.applySettings(cfg)
		if got := statuses(); !slices.Equal(got, step.want) {
			t.Errorf("to %s: %q, want %q", step.mode, got, step.want)
		}
	}
}
