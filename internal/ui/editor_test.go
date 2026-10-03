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
	var commands []Command
	for _, c := range cmds {
		commands = append(commands, Command{Name: c, Description: c + " it"})
	}
	m := newEditorModel(commands, nil)
	m.footer = "~/repo · test"
	return m
}

// press types keys and returns what the last asked of the screen.
func press(m *editorModel, keys ...tea.KeyMsg) editorAction {
	var act editorAction
	for _, k := range keys {
		_, act = m.update(k)
	}
	return act
}

func view(m *editorModel) string { return strings.Join(m.view(m.width, m.height, true), "\n") }

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
	if press(m, key(tea.KeyEnter)) != editSubmit {
		t.Fatal("enter did not submit")
	}
	if text, shown := m.take(); text != "one!x\ntwo\nthree" || shown != text || !m.empty() {
		t.Fatalf("took %q, %q, left %q", text, shown, m.text())
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
	act := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\r\nb"), Paste: true}, key(tea.KeyEnter))
	if got := m.expanded(); got != "a\nb" || act != editSubmit {
		t.Fatalf("small paste = %q, submitted %v", got, act == editSubmit)
	}
}

// TestEditorPasteMarkerIsAtomic: no deletion or cursor move leaves part of
// a marker behind, and a deleted marker's paste is gone with it.
func TestEditorPasteMarkerIsAtomic(t *testing.T) {
	big := strings.Repeat("line\n", 15) + "end"
	paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(big), Paste: true}
	alt := func(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t, Alt: true} }
	for name, keys := range map[string][]tea.KeyMsg{
		"ctrl+w":        {key(tea.KeyCtrlW)},
		"alt+backspace": {alt(tea.KeyBackspace)},
		"ctrl+u":        {key(tea.KeyCtrlU)},
		"ctrl+k":        {key(tea.KeyCtrlA), runes(" "), key(tea.KeyCtrlA), key(tea.KeyCtrlK)},
		"delete":        {key(tea.KeyCtrlA), key(tea.KeyRight), key(tea.KeyDelete)},
	} {
		m := testEditor()
		press(m, runes("x"), paste)
		press(m, keys...)
		if got := m.text(); strings.Contains(got, "paste") || strings.Contains(got, "lines]") {
			t.Errorf("%s left %q", name, got)
		}
		if len(m.pastes) != 0 {
			t.Errorf("%s kept the paste", name)
		}
		if press(m, runes("[paste #1 +16 lines]")); m.expanded() != m.text() {
			t.Errorf("%s: typing the marker brought the paste back", name)
		}
	}

	m := testEditor()
	press(m, runes("a "), paste, runes(" b"))
	end := m.col
	press(m, key(tea.KeyLeft), key(tea.KeyLeft), alt(tea.KeyLeft))
	if m.col != 2 {
		t.Errorf("alt+left stopped at %d, want 2 (the marker's start)", m.col)
	}
	press(m, alt(tea.KeyRight))
	if m.col != end-2 {
		t.Errorf("alt+right stopped at %d, want %d (the marker's end)", m.col, end-2)
	}

	// Markers wrapped over rows, between lines: no move rests inside one.
	m = testEditor()
	m.setSize(14, 20)
	press(m, runes("0123456789"), key(tea.KeyCtrlJ), runes("abcd"), paste, runes(" efgh"), key(tea.KeyCtrlJ), runes("0123456789"))
	moves := []tea.KeyMsg{key(tea.KeyUp), key(tea.KeyDown), key(tea.KeyLeft), key(tea.KeyRight), alt(tea.KeyLeft), alt(tea.KeyRight)}
	for i := range 400 {
		k := moves[(i*7+i/5)%len(moves)]
		press(m, k)
		for _, r := range m.markers() {
			if r[0] < m.col && m.col < r[1] {
				t.Fatalf("move %d (%s) left the cursor at %d:%d, inside %v", i, k, m.row, m.col, r)
			}
		}
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
	if !strings.Contains(ansi.Strip(view(m)), "/quit  quit it") {
		t.Fatalf("popup not drawn:\n%s", ansi.Strip(view(m)))
	}
	if act := press(m, key(tea.KeyEnter)); act != editSubmit || m.text() != "/quit" {
		t.Fatalf("one enter: submitted %v, text %q", act == editSubmit, m.text())
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
	if act := press(m, runes("draft"), key(tea.KeyCtrlC)); m.text() != "" || act != editNothing {
		t.Fatalf("ctrl+c on text: %q, %v", m.text(), act)
	}
	if act := press(m, key(tea.KeyCtrlC)); act != editNothing || !strings.Contains(ansi.Strip(view(m)), "press ctrl+c again to quit") {
		t.Fatal("first ctrl+c on empty input should only hint")
	}
	if press(m, key(tea.KeyCtrlC)) != editQuit {
		t.Fatal("second ctrl+c did not quit")
	}
	m = testEditor()
	if press(m, key(tea.KeyCtrlD)) != editQuit {
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

	m := newEditorModel(nil, h)
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
	v := ansi.Strip(view(m))
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

func undo(m *editorModel) { press(m, key(tea.KeyCtrlUnderscore)) }

// TestEditorUndo: a word typed is one step with the space before it, a
// run of backspaces one step, and deletions, pastes and recalls each one.
func TestEditorUndo(t *testing.T) {
	m := testEditor()
	for _, r := range "hello big world" {
		press(m, runes(string(r)))
	}
	for _, want := range []string{"hello big", "hello", "", ""} {
		if undo(m); m.text() != want {
			t.Fatalf("undo: %q, want %q", m.text(), want)
		}
	}

	// A move ends a word: typing after it is a step of its own.
	press(m, runes("ab"), key(tea.KeyLeft), runes("x"))
	if undo(m); m.text() != "ab" || m.col != 1 {
		t.Fatalf("undo after a move: %q col %d", m.text(), m.col)
	}

	m = testEditor()
	press(m, runes("one two"), key(tea.KeyCtrlJ), runes("three"))
	for _, k := range []tea.KeyMsg{key(tea.KeyCtrlU), key(tea.KeyCtrlW), key(tea.KeyCtrlK), key(tea.KeyCtrlC)} {
		before, col := m.text(), m.col
		if k.Type == tea.KeyCtrlK {
			press(m, key(tea.KeyCtrlA))
			col = 0
		}
		press(m, k)
		if m.text() == before {
			t.Fatalf("%s changed nothing", k)
		}
		if undo(m); m.text() != before || m.col != col {
			t.Fatalf("undo %s: %q col %d, want %q col %d", k, m.text(), m.col, before, col)
		}
	}

	// A run of backspaces is one step.
	press(m, key(tea.KeyCtrlE))
	for range 4 {
		press(m, key(tea.KeyBackspace))
	}
	if undo(m); m.text() != "one two\nthree" {
		t.Fatalf("undo backspaces: %q", m.text())
	}

	// A paste and the cut of its marker come back with the paste.
	big := strings.Repeat("line\n", 15) + "end"
	press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(big), Paste: true})
	marked := m.text()
	press(m, key(tea.KeyBackspace))
	if undo(m); m.text() != marked || m.expanded() != "one two\nthree"+big {
		t.Fatalf("undo the marker's cut: %q", m.text())
	}
	if undo(m); m.text() != "one two\nthree" || len(m.pastes) != 0 {
		t.Fatalf("undo the paste: %q, %d pastes", m.text(), len(m.pastes))
	}

	// Going back through the history is one step back to the draft.
	h := LoadHistory(filepath.Join(t.TempDir(), "history"))
	h.Add("old one")
	h.Add("old two")
	m = newEditorModel(nil, h)
	press(m, key(tea.KeyUp), key(tea.KeyUp))
	if m.text() != "old one" {
		t.Fatalf("history: %q", m.text())
	}
	press(m, runes("!"))
	undo(m)
	if undo(m); m.text() != "" || m.hist != 2 {
		t.Fatalf("undo the recall: %q, hist %d", m.text(), m.hist)
	}

	// The $EDITOR result, and nothing after a send.
	m = testEditor()
	press(m, runes("draft"))
	f := filepath.Join(t.TempDir(), "msg.md")
	os.WriteFile(f, []byte("edited\n"), 0o600)
	m.update(editedMsg{path: f})
	if m.text() != "edited" {
		t.Fatalf("edited: %q", m.text())
	}
	if undo(m); m.text() != "draft" {
		t.Fatalf("undo the edit: %q", m.text())
	}
	m.take()
	if undo(m); m.text() != "" {
		t.Fatalf("undo after send: %q", m.text())
	}

	// The stack is capped.
	for i := range undoMax + 20 {
		press(m, runes(fmt.Sprint(i%10)), runes(" "))
	}
	if len(m.undo) != undoMax {
		t.Fatalf("%d steps kept", len(m.undo))
	}
}
