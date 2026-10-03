package ui

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSelectActions: an action key picks the selected item and says so;
// the hint line names the keys; ctrl+d is an action, not the filter's
// delete; with no match the key does nothing.
func TestSelectActions(t *testing.T) {
	sel := Select{
		Title: "Resume a chat", Filter: true,
		Items:   []Item{{Label: "first", Value: "0"}, {Label: "second", Value: "1"}},
		Actions: []Action{{"ctrl+r", "rename"}, {"ctrl+d", "delete"}},
	}
	m := newSelectModel(sel)
	m.setSize(100, 20)
	if v := plain(m); !strings.Contains(v, "ctrl+r rename · ctrl+d delete · esc cancel") {
		t.Fatalf("hints:\n%s", v)
	}
	send(m, key(tea.KeyDown), key(tea.KeyCtrlR))
	if !m.done || m.chosen == nil || m.chosen.Value != "1" || m.action != "ctrl+r" {
		t.Fatalf("ctrl+r: done %v, chosen %+v, action %q", m.done, m.chosen, m.action)
	}

	m = newSelectModel(sel)
	send(m, runes("sec"), key(tea.KeyLeft), key(tea.KeyCtrlD))
	if m.chosen == nil || m.chosen.Value != "1" || m.action != "ctrl+d" || m.filter.String() != "sec" {
		t.Fatalf("ctrl+d: chosen %+v, action %q, filter %q", m.chosen, m.action, m.filter.String())
	}

	m = newSelectModel(sel)
	send(m, runes("zzz"), key(tea.KeyCtrlD))
	if m.done {
		t.Fatal("an action with no match ended the list")
	}
	send(m, key(tea.KeyEsc), key(tea.KeyEnter))
	if m.chosen == nil || m.action != "" {
		t.Fatalf("enter: chosen %+v, action %q", m.chosen, m.action)
	}
}

// TestEscEsc: two Escs (or both at once, alt+esc) on an empty input while
// nothing runs send the command; one Esc, a slow second, typed text or a
// running answer do not.
func TestEscEsc(t *testing.T) {
	m := testScreen(60, 12)
	m.escCmd = "/edit"
	esc := key(tea.KeyEsc)
	m.Update(esc)
	if len(m.queue) != 0 {
		t.Fatal("one Esc sent the command")
	}
	m.Update(esc)
	if len(m.queue) != 1 || m.queue[0].text != "/edit" {
		t.Fatalf("queue after Esc Esc: %v", m.queue)
	}

	m.queue = nil
	reply := make(chan [2]string, 1)
	m.waiter = reply
	m.Update(tea.KeyMsg{Type: tea.KeyEscape, Alt: true})
	if got := <-reply; got[0] != "/edit" {
		t.Fatalf("alt+esc sent %v", got)
	}

	m.Update(esc)
	m.escAt = time.Now().Add(-time.Second)
	m.Update(esc)
	if len(m.queue) != 0 {
		t.Fatal("a slow second Esc sent the command")
	}
	m.escAt = time.Time{}
	m.Update(esc)
	m.Update(runes("x"))
	m.Update(esc)
	if len(m.queue) != 0 || m.ed.text() != "x" {
		t.Fatalf("Esc around typing: queue %v, text %q", m.queue, m.ed.text())
	}
	m.ed.setText("")
	cancelled := 0
	m.cancel = func() { cancelled++ }
	m.Update(esc)
	m.Update(esc)
	if len(m.queue) != 0 || cancelled != 1 {
		t.Fatalf("Esc while running: queue %v, cancelled %d", m.queue, cancelled)
	}
}

// TestFocusReports: ttyInput keeps the focus reports from bubbletea,
// wherever they fall in a read, and records them.
func TestFocusReports(t *testing.T) {
	var blurred atomic.Bool
	in := &ttyInput{blurred: &blurred}
	if out, _ := in.translate([]byte("a\x1b[Ob")); string(out) != "ab" || !blurred.Load() {
		t.Fatalf("blur: %q, blurred %v", out, blurred.Load())
	}
	if out, _ := in.translate([]byte("\x1b[I\x1b[A")); string(out) != "\x1b[A" || blurred.Load() {
		t.Fatalf("focus: %q, blurred %v", out, blurred.Load())
	}
	m := testScreen(40, 10)
	m.Update(tea.BlurMsg{})
	if !m.blurred.Load() {
		t.Fatal("BlurMsg not recorded")
	}
	m.Update(tea.FocusMsg{})
	if m.blurred.Load() {
		t.Fatal("FocusMsg not recorded")
	}
}

func TestCleanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"docsgpt · fix the build · repo":     "docsgpt · fix the build · repo",
		"evil\x1b]0;owned\x07 title\n\tnext": "evil ]0;owned title next",
		"\x9b31m c1 ​":                       "31m c1",
		strings.Repeat("é", 100):             strings.Repeat("é", titleMax-1) + "…",
	} {
		if got := cleanTitle(in); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNotification(t *testing.T) {
	for _, env := range []string{"TMUX", "STY", "TERM_PROGRAM", "TERM", "KITTY_WINDOW_ID", "VTE_VERSION"} {
		t.Setenv(env, "")
	}
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	if got := notification("DocsGPT", "Answer\x07 ready"); got != "\x1b]9;DocsGPT: Answer ready\a" {
		t.Errorf("iTerm2: %q", got)
	}
	t.Setenv("TERM_PROGRAM", "ghostty")
	if got := notification("DocsGPT", "a;b"); got != "\x1b]777;notify;DocsGPT;a,b\a" {
		t.Errorf("ghostty: %q", got)
	}
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	if got := notification("DocsGPT", "x"); got != "\a" {
		t.Errorf("Terminal.app: %q", got)
	}
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	if got := notification("DocsGPT", "x"); got != "\a" {
		t.Errorf("tmux: %q", got)
	}
}
