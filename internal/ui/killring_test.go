package ui

import (
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/attach"

	tea "github.com/charmbracelet/bubbletea"
)

func altKey(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t, Alt: true} }
func altRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Alt: true}
}

func ringTexts(m *editorModel) []string {
	var out []string
	for _, k := range m.ring {
		out = append(out, k.text)
	}
	return out
}

// TestKillRingKills: every kill key puts what it took on the ring, and a
// run of kills makes one entry, in the order the text had.
func TestKillRingKills(t *testing.T) {
	for _, c := range []struct {
		name, want, left string
		keys             []tea.KeyMsg
	}{
		{"ctrl+k", "two three", "one ", []tea.KeyMsg{key(tea.KeyCtrlA), altRunes("f"), key(tea.KeyRight), key(tea.KeyCtrlK)}},
		{"ctrl+u", "one two ", "three", []tea.KeyMsg{key(tea.KeyCtrlA), altRunes("f"), altRunes("f"), key(tea.KeyRight), key(tea.KeyCtrlU)}},
		{"ctrl+w", "three", "one two ", []tea.KeyMsg{key(tea.KeyCtrlW)}},
		{"alt+backspace", "three", "one two ", []tea.KeyMsg{altKey(tea.KeyBackspace)}},
		{"alt+d", "one", " two three", []tea.KeyMsg{key(tea.KeyCtrlA), altRunes("d")}},
		{"alt+delete", "one", " two three", []tea.KeyMsg{key(tea.KeyCtrlA), altKey(tea.KeyDelete)}},
		// Forward kills append, backward ones prepend.
		{"alt+d twice", "one two", " three", []tea.KeyMsg{key(tea.KeyCtrlA), altRunes("d"), altRunes("d")}},
		{"ctrl+w twice", "two three", "one ", []tea.KeyMsg{key(tea.KeyCtrlW), key(tea.KeyCtrlW)}},
		{"ctrl+w then ctrl+u", "one two three", "", []tea.KeyMsg{key(tea.KeyCtrlW), key(tea.KeyCtrlU)}},
	} {
		m := testEditor()
		press(m, runes("one two three"))
		press(m, c.keys...)
		if got := ringTexts(m); len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: ring %q, want [%q]", c.name, got, c.want)
		}
		if m.text() != c.left {
			t.Errorf("%s: left %q, want %q", c.name, m.text(), c.left)
		}
	}

	// A move between kills starts a new entry.
	m := testEditor()
	press(m, runes("one two three"), key(tea.KeyCtrlW), key(tea.KeyLeft), key(tea.KeyCtrlW))
	if got := ringTexts(m); strings.Join(got, "|") != "three|two" {
		t.Errorf("kills a move apart: %q", got)
	}

	// At a line's edge a kill takes the line break.
	m = testEditor()
	press(m, runes("ab"), key(tea.KeyCtrlJ), runes("cd"), key(tea.KeyCtrlU), key(tea.KeyCtrlU))
	if got := ringTexts(m); len(got) != 1 || got[0] != "\ncd" || m.text() != "ab" {
		t.Errorf("ctrl+u over the line break: ring %q, text %q", got, m.text())
	}
	m = testEditor()
	press(m, runes("ab"), key(tea.KeyCtrlJ), runes("cd"), key(tea.KeyUp), key(tea.KeyCtrlA), key(tea.KeyCtrlK), key(tea.KeyCtrlK))
	if got := ringTexts(m); len(got) != 1 || got[0] != "ab\n" || m.text() != "cd" {
		t.Errorf("ctrl+k over the line break: ring %q, text %q", got, m.text())
	}

	// Nothing to kill, nothing on the ring.
	m = testEditor()
	press(m, key(tea.KeyCtrlK), key(tea.KeyCtrlU), key(tea.KeyCtrlW), altRunes("d"))
	if len(m.ring) != 0 {
		t.Errorf("kills of nothing: ring %q", ringTexts(m))
	}

	// The ring is capped.
	m = testEditor()
	for range killMax + 5 {
		press(m, runes("x"), key(tea.KeyCtrlW), runes(" "))
	}
	if len(m.ring) != killMax {
		t.Errorf("%d kills kept", len(m.ring))
	}
}

// TestKillRingYank: Ctrl+Y puts back the latest kill, Alt+Y right after
// it goes back round the ring, and undo takes each step back.
func TestKillRingYank(t *testing.T) {
	m := testEditor()
	press(m, key(tea.KeyCtrlY), altRunes("y"))
	if m.text() != "" {
		t.Fatalf("yank of an empty ring: %q", m.text())
	}

	press(m, runes("alpha"), key(tea.KeyCtrlW), runes("beta"), key(tea.KeyCtrlW), runes("gamma"), key(tea.KeyCtrlW))
	press(m, runes("x "), key(tea.KeyCtrlY))
	if m.text() != "x gamma" || m.col != 7 {
		t.Fatalf("yank: %q col %d", m.text(), m.col)
	}
	for _, want := range []string{"x beta", "x alpha", "x gamma", "x beta"} {
		if press(m, altRunes("y")); m.text() != want {
			t.Fatalf("yank-pop: %q, want %q", m.text(), want)
		}
	}
	if undo(m); m.text() != "x gamma" {
		t.Fatalf("undo a yank-pop: %q", m.text())
	}

	// Alt+Y only follows a yank.
	m = testEditor()
	press(m, runes("one"), key(tea.KeyCtrlW), runes("two"), key(tea.KeyCtrlW), key(tea.KeyCtrlY), key(tea.KeyLeft), altRunes("y"))
	if m.text() != "two" {
		t.Fatalf("yank-pop after a move: %q", m.text())
	}

	// A yank is one undo step, and a multi-line kill comes back as lines.
	m = testEditor()
	press(m, runes("ab"), key(tea.KeyCtrlJ), runes("cd"), key(tea.KeyCtrlU), key(tea.KeyCtrlU), runes("!"), key(tea.KeyCtrlY))
	if m.text() != "ab!\ncd" || m.row != 1 || m.col != 2 {
		t.Fatalf("multi-line yank: %q at %d:%d", m.text(), m.row, m.col)
	}
	if undo(m); m.text() != "ab!" {
		t.Fatalf("undo a yank: %q", m.text())
	}

	// The ring outlives a send.
	press(m, key(tea.KeyCtrlU))
	m.take()
	if press(m, key(tea.KeyCtrlY)); m.text() != "ab!" {
		t.Fatalf("yank after a send: %q", m.text())
	}
}

// TestKillRingMarkers: a killed paste or file marker comes back with its
// paste or file, numbered after those the editor holds by then.
func TestKillRingMarkers(t *testing.T) {
	big := strings.Repeat("line\n", 15) + "end"
	paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(big), Paste: true}

	m := testEditor()
	press(m, runes("a "), paste, key(tea.KeyCtrlW))
	if m.text() != "a " || len(m.pastes) != 0 {
		t.Fatalf("kill of the marker: %q, %d pastes", m.text(), len(m.pastes))
	}
	press(m, paste) // takes #1 again
	press(m, key(tea.KeyCtrlY))
	if got := m.text(); got != "a [paste #1 +16 lines][paste #2 +16 lines]" {
		t.Fatalf("yanked marker: %q", got)
	}
	if want := "a " + big + big; m.expanded() != want {
		t.Fatalf("yanked paste: %q", m.expanded())
	}

	m = testEditor()
	f := attach.File{Path: "/tmp/shot.png", Name: "shot.png", Image: true, Size: 2048}
	m.attach([]attach.File{f})
	m.changed()
	press(m, key(tea.KeyCtrlU))
	if len(m.attached()) != 0 {
		t.Fatalf("kill of a file marker kept it: %q", m.text())
	}
	m.attach([]attach.File{{Path: "/tmp/b.txt", Name: "b.txt", Size: 10}})
	m.changed()
	press(m, key(tea.KeyCtrlY))
	got := m.attached()
	if len(got) != 2 || got[1].Path != "/tmp/shot.png" || got[1].ID != 2 {
		t.Fatalf("yanked file: %+v in %q", got, m.text())
	}
	if !strings.Contains(m.text(), "[image #2 · shot.png · 2 KB]") {
		t.Fatalf("yanked file marker: %q", m.text())
	}

	// Alt+Y away from a yanked marker drops its paste.
	m = testEditor()
	press(m, paste, key(tea.KeyCtrlW), runes("word"), key(tea.KeyCtrlW), key(tea.KeyCtrlY), altRunes("y"))
	if m.text() != "[paste #1 +16 lines]" || m.expanded() != big {
		t.Fatalf("yank-pop to a paste: %q", m.text())
	}
	press(m, altRunes("y"))
	if m.text() != "word" || len(m.pastes) != 0 {
		t.Fatalf("yank-pop past a paste: %q, %d pastes", m.text(), len(m.pastes))
	}
}
