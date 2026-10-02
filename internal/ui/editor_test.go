package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func testEditor(cmds ...string) *editorModel {
	e := &Editor{Footer: "~/repo · test"}
	for _, c := range cmds {
		e.Commands = append(e.Commands, Command{Name: c, Description: c + " it"})
	}
	return newEditorModel(e)
}

func press(m *editorModel, keys ...tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = m.Update(k)
	}
	return cmd
}

func TestEditorMultiline(t *testing.T) {
	m := testEditor()
	press(m, runes("one"), key(tea.KeyCtrlJ), runes("two\\"), key(tea.KeyEnter), runes("three"))
	if got := m.text(); got != "one\ntwo\nthree" {
		t.Fatalf("text = %q", got)
	}
	press(m, key(tea.KeyUp), key(tea.KeyUp), key(tea.KeyEnd), runes("!"), tea.KeyMsg{Type: tea.KeyEnter, Alt: true}, runes("x"))
	if got := m.text(); got != "one!\nx\ntwo\nthree" {
		t.Fatalf("after alt+enter: %q", got)
	}
	press(m, key(tea.KeyCtrlA), key(tea.KeyBackspace))
	if got := m.text(); got != "one!x\ntwo\nthree" {
		t.Fatalf("backspace at line start: %q", got)
	}
	if cmd := press(m, key(tea.KeyEnter)); cmd == nil || !m.done || m.quit {
		t.Fatal("enter did not submit")
	}
}

func TestEditorPasteMarker(t *testing.T) {
	m := testEditor()
	big := strings.Repeat("line\n", 199) + "end"
	press(m, runes("see "), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(big), Paste: true}, runes(" ok"))
	if got := m.text(); got != "see [paste #1 +200 lines] ok" {
		t.Fatalf("text = %q", got)
	}
	if got := m.expanded(); got != "see "+big+" ok" {
		t.Fatalf("expanded paste lost: %q", got[:40])
	}
	// The marker is one unit for the cursor and backspace.
	press(m, key(tea.KeyLeft), key(tea.KeyLeft), key(tea.KeyLeft), key(tea.KeyLeft))
	if m.col != 4 {
		t.Fatalf("cursor at %d, want 4 (before the marker)", m.col)
	}
	press(m, key(tea.KeyRight), key(tea.KeyBackspace))
	if got := m.text(); got != "see  ok" {
		t.Fatalf("after deleting the marker: %q", got)
	}
	m = testEditor()
	press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\r\nb"), Paste: true}, key(tea.KeyEnter))
	if got := m.expanded(); got != "a\nb" || !m.done {
		t.Fatalf("small paste = %q, submitted %v", got, m.done)
	}
}

func TestEditorSlashPopup(t *testing.T) {
	m := testEditor("new", "copy", "quit", "export")
	press(m, runes("/"))
	if m.popup == nil || len(m.popup.matches) != 4 {
		t.Fatal("/ did not show every command")
	}
	press(m, runes("q"))
	if len(m.popup.matches) != 1 || m.selected() != "quit" {
		t.Fatalf("/q matches %v", m.popup.matches)
	}
	if !strings.Contains(ansi.Strip(m.View()), "/quit  quit it") {
		t.Fatalf("popup not drawn:\n%s", ansi.Strip(m.View()))
	}
	if press(m, key(tea.KeyEnter)); !m.done || m.text() != "/quit" {
		t.Fatalf("one enter: done %v, text %q", m.done, m.text())
	}

	m = testEditor("new", "copy", "quit", "export")
	press(m, runes("/e"), key(tea.KeyTab))
	if m.text() != "/export " || m.popup != nil {
		t.Fatalf("tab: %q, popup %v", m.text(), m.popup != nil)
	}
	m = testEditor("new", "copy")
	press(m, runes("/c"), key(tea.KeyEsc))
	if m.popup != nil {
		t.Fatal("esc kept the popup")
	}
	press(m, runes("o"))
	if m.popup == nil {
		t.Fatal("typing did not bring the popup back")
	}
}

func TestEditorCtrlC(t *testing.T) {
	m := testEditor()
	press(m, runes("draft"), key(tea.KeyCtrlC))
	if m.text() != "" || m.done {
		t.Fatalf("ctrl+c on text: %q, done %v", m.text(), m.done)
	}
	press(m, key(tea.KeyCtrlC))
	if m.done || !strings.Contains(ansi.Strip(m.View()), "press ctrl+c again to quit") {
		t.Fatal("first ctrl+c on empty input should only hint")
	}
	press(m, key(tea.KeyCtrlC))
	if !m.done || !m.quit {
		t.Fatal("second ctrl+c did not quit")
	}
	m = testEditor()
	if press(m, key(tea.KeyCtrlD)); !m.quit {
		t.Fatal("ctrl+d on empty input did not quit")
	}
}

func TestEditorHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "history")
	h := LoadHistory(path)
	for _, s := range []string{"first", "multi\nline", "my key is 0b6e2c1a-7d4f-4e8a-9c3b-5f1d2e3a4b5c", "dgpt_pat_abc", "last"} {
		h.Add(s)
	}
	h = LoadHistory(path)
	if want := []string{"first", "multi\nline", "last"}; strings.Join(h.entries, "|") != strings.Join(want, "|") {
		t.Fatalf("entries = %q", h.entries)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("history file mode: %v %v", fi.Mode(), err)
	}

	m := newEditorModel(&Editor{History: h})
	press(m, runes("dra"), key(tea.KeyUp))
	if m.text() != "dra" || m.col != 0 {
		t.Fatalf("up on a draft goes to its start first: %q col %d", m.text(), m.col)
	}
	press(m, key(tea.KeyUp))
	if m.text() != "last" {
		t.Fatalf("up: %q", m.text())
	}
	press(m, key(tea.KeyUp))
	if m.text() != "multi\nline" || m.row != 1 {
		t.Fatalf("up: %q row %d", m.text(), m.row)
	}
	press(m, key(tea.KeyUp), key(tea.KeyUp))
	if m.text() != "first" {
		t.Fatalf("up through a multi-line entry: %q", m.text())
	}
	press(m, key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyDown))
	if m.text() != "dra" {
		t.Fatalf("down back to the draft: %q", m.text())
	}
}

// TestHistoryRewrite: trimming the history replaces the file whole, private,
// with no temporary file left.
func TestHistoryRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history")
	h := LoadHistory(path)
	for i := 0; i <= historyMax+historyMax/2; i++ {
		h.Add(fmt.Sprintf("entry %d", i))
	}
	if got := LoadHistory(path).entries; len(got) != historyMax || got[len(got)-1] != fmt.Sprintf("entry %d", historyMax+historyMax/2) {
		t.Fatalf("%d entries, last %q", len(got), got[len(got)-1])
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("history file mode: %v %v", fi, err)
	}
	if files, _ := os.ReadDir(dir); len(files) != 1 {
		t.Fatalf("files left: %v", files)
	}
}

func TestEditorWrapsAndScrolls(t *testing.T) {
	m := testEditor()
	m.setSize(20, 20)
	press(m, runes(strings.Repeat("x", 45)))
	rows := m.layout()
	if len(rows) != 3 || m.cursorRow(rows) != 2 {
		t.Fatalf("rows %v, cursor row %d", rows, m.cursorRow(rows))
	}
	press(m, key(tea.KeyUp))
	if m.row != 0 || m.col != 26 {
		t.Fatalf("up within a wrapped line: col %d", m.col)
	}
	press(m, key(tea.KeyCtrlE))
	for i := 0; i < 10; i++ {
		press(m, key(tea.KeyCtrlJ), runes("l"))
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "↑ 7 more") || strings.Count(v, "\n") != 8 {
		t.Fatalf("scrolled view:\n%s", v)
	}
}

// TestEditorSetTextPrintable checks text the editor did not see typed (a
// history entry, the $EDITOR result) loses its control characters.
func TestEditorSetTextPrintable(t *testing.T) {
	m := testEditor()
	m.setText("red \x1b[31mtext\r\n\ttab\x07")
	if got := m.text(); got != "red [31mtext\n    tab" {
		t.Fatalf("setText kept controls: %q", got)
	}
}
