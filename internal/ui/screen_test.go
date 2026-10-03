package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// numbered is a block of n lines "<name> <i>", wrapped in two at widths
// under 30.
type numbered struct {
	name string
	n    int
}

func (b *numbered) Lines(width int) []string {
	var out []string
	for i := range b.n {
		out = append(out, fmt.Sprintf("%s %d", b.name, i))
		if width < 30 {
			out = append(out, "  (wrapped)")
		}
	}
	return out
}

type prompt struct{ numbered }

func (prompt) Prompt() {}

func testScreen(w, h int, blocks ...Block) *screenModel {
	m := &screenModel{ed: newEditorModel(nil, nil), follow: true, blocks: blocks}
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func rows(m *screenModel) []string { return strings.Split(ansi.Strip(m.View()), "\n") }

func wheel(m *screenModel, up bool, n int) {
	b := tea.MouseButtonWheelDown
	if up {
		b = tea.MouseButtonWheelUp
	}
	for range n {
		m.wheel = time.Time{}
		m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: b})
	}
}

// TestScreenLayout: the transcript on top, the blocks a blank line apart,
// then the status row and the editor; every frame fills the window.
func TestScreenLayout(t *testing.T) {
	m := testScreen(40, 12, &numbered{"a", 2}, &numbered{"empty", 0}, &numbered{"b", 1})
	r := rows(m)
	if len(r) != 12 || r[0] != "a 0" || r[1] != "a 1" || r[2] != "" || r[3] != "b 0" || !strings.HasPrefix(r[8], "───") {
		t.Fatalf("frame:\n%s", strings.Join(r, "\n"))
	}
}

// TestScreenScroll: the view follows the end, stays where the user
// scrolled while lines come, says how many came, and jumps back.
func TestScreenScroll(t *testing.T) {
	answer := &numbered{"line", 30}
	m := testScreen(40, 12, answer) // 7 transcript rows
	if r := rows(m); r[6] != "line 29" {
		t.Fatalf("not at the end: %q", r[:7])
	}
	if perLineWheel {
		wheel(m, true, 6)
	} else {
		wheel(m, true, 2)
	}
	r := rows(m)
	if r[0] != "line 17" || m.follow {
		t.Fatalf("after scrolling up: %q", r[:7])
	}
	answer.n = 40
	r = rows(m)
	if r[0] != "line 17" || !strings.Contains(r[7], "↓ 10 new lines · end to jump") {
		t.Fatalf("the view moved, or no hint: %q / %q", r[0], r[7])
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if r = rows(m); r[0] != "line 11" {
		t.Fatalf("pgup: %q", r[0])
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if r = rows(m); !strings.Contains(r[7], "ctrl+end to jump") {
		t.Fatalf("with text typed, end is the editor's: %q", r[7])
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlEnd})
	if r = rows(m); r[6] != "line 39" || !m.follow || strings.TrimSpace(r[7]) != "" {
		t.Fatalf("ctrl+end: %q, %q", r[6], r[7])
	}
	m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	if rows(m); !m.follow {
		t.Fatal("back at the end without following it")
	}
}

// TestScreenResizeKeepsPlace: a scrolled-back view shows the same line
// after the transcript is wrapped again.
func TestScreenResizeKeepsPlace(t *testing.T) {
	m := testScreen(40, 12, &numbered{"x", 5}, &numbered{"line", 30})
	rows(m)
	m.scroll(-20)
	r := rows(m)
	top := r[0]
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 12})
	if r = rows(m); r[0] != top || m.follow {
		t.Fatalf("narrower: %q, want %q", r[0], top)
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if r = rows(m); r[0] != top {
		t.Fatalf("wider again: %q, want %q", r[0], top)
	}
}

// fold is an Expander: a title and 3 of its n lines, all of them expanded.
type fold struct {
	name     string
	n        int
	expanded bool
}

func (f *fold) Expand(on bool) { f.expanded = on }
func (f *fold) Folds() bool    { return f.n > 3 }
func (f *fold) Lines(int) []string {
	out := []string{"$ " + f.name}
	for i := range f.n {
		if f.expanded || i >= f.n-3 {
			out = append(out, fmt.Sprintf("%s out %d", f.name, i))
		}
	}
	return out
}

// TestScreenExpand: Ctrl+O expands and collapses every Expander, new ones
// too; the view stays at the end, or on the line at its top, or on the
// start of the block at its top when that one changed.
func TestScreenExpand(t *testing.T) {
	stubClipboard(t)
	f1, f2 := &fold{name: "f1", n: 20}, &fold{name: "f2", n: 2}
	m := testScreen(70, 12, &numbered{"a", 10}, f1, f2, &numbered{"b", 10}) // 7 transcript rows
	rows(m)
	ctrlO := func() { m.Update(tea.KeyMsg{Type: tea.KeyCtrlO}) }

	ctrlO()
	r := rows(m)
	if !f1.expanded || !f2.expanded || !m.follow || r[6] != "b 9" || !strings.Contains(r[7], "Tool output expanded") {
		t.Fatalf("expanded at the end: %q / %q", r[:7], r[7])
	}
	f3 := &fold{name: "f3", n: 9}
	m.Update(doMsg(func(m *screenModel) tea.Cmd { // as Screen.Add
		(&Screen{m: m, headless: true}).Add(f3)
		return nil
	}))
	if !f3.expanded {
		t.Fatal("a new block was not expanded")
	}

	// The top in a block below f1: on the same line.
	m.top, m.follow = m.offset(3)+4, false
	if r = rows(m); r[0] != "b 4" {
		t.Fatalf("setup: %q", r[0])
	}
	ctrlO()
	if r = rows(m); r[0] != "b 4" || f1.expanded || !strings.Contains(r[7], "Tool output collapsed") {
		t.Fatalf("collapsed: top %q, status %q", r[0], r[7])
	}

	// The top inside f1: from its start.
	m.top = m.offset(1) + 2
	if r = rows(m); r[0] != "f1 out 18" {
		t.Fatalf("setup: %q", r[0])
	}
	ctrlO()
	if r = rows(m); r[0] != "$ f1" || r[1] != "f1 out 0" || m.follow {
		t.Fatalf("expanded inside: %q", r[:3])
	}
	// The top in f2, which shows the same either way: on its line.
	m.top = m.offset(2) + 1
	ctrlO()
	if r = rows(m); r[0] != "f2 out 0" {
		t.Fatalf("a block that does not fold: %q", r[:3])
	}
}

// TestScreenJump: Ctrl+↑/↓ move between the user's messages.
func TestScreenJump(t *testing.T) {
	m := testScreen(40, 12, &prompt{numbered{"q1", 1}}, &numbered{"a", 20}, &prompt{numbered{"q2", 1}}, &numbered{"b", 20})
	rows(m)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlUp})
	if r := rows(m); r[0] != "q2 0" {
		t.Fatalf("ctrl+up: %q", r[0])
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlUp})
	if r := rows(m); r[0] != "q1 0" {
		t.Fatalf("ctrl+up twice: %q", r[0])
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlDown})
	if r := rows(m); r[0] != "q2 0" {
		t.Fatalf("ctrl+down: %q", r[0])
	}
}

// TestScreenSubmitAndQueue: Enter hands the message to the waiting caller,
// or queues it while the caller is busy; Esc stops the work and puts what
// was queued back in the editor.
func TestScreenSubmitAndQueue(t *testing.T) {
	m := testScreen(40, 12)
	waiter := make(chan [2]string, 1)
	m.waiter = waiter
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := <-waiter; got[0] != "hi" || !m.ed.empty() {
		t.Fatalf("submitted %q, editor %q", got, m.ed.text())
	}
	cancelled := false
	m.cancel = func() { cancelled = true }
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("next")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.queue) != 1 || !strings.Contains(rows(m)[7], "1 queued") {
		t.Fatalf("not queued: %v", m.queue)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !cancelled || len(m.queue) != 0 || m.ed.text() != "next" {
		t.Fatalf("esc: cancelled %v, queue %v, editor %q", cancelled, m.queue, m.ed.text())
	}
}

// TestScreenPanel: a prompt that opens while the user types during an
// answer takes the keys only after a moment, so they do not answer it; one
// the user asked for takes them at once.
func TestScreenPanel(t *testing.T) {
	m := testScreen(60, 16)
	m.cancel = func() {}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	p := &panel{sel: newSelectModel(approvalSelect()), reply: make(chan result, 1)}
	m.Update(doMsg(func(m *screenModel) tea.Cmd { return m.open(p) }))
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if m.ed.text() != "xa" || len(p.reply) != 0 {
		t.Fatal("a key typed as the panel opened answered it")
	}
	if !strings.Contains(strings.Join(rows(m), "\n"), "Approve") {
		t.Fatal("panel not drawn")
	}
	p.until = time.Time{}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if r := <-p.reply; r.value != "deny" || m.panel != nil {
		t.Fatalf("answer %v", r)
	}

	m.cancel, m.typed = nil, time.Time{}
	p = &panel{sel: newSelectModel(approvalSelect()), reply: make(chan result, 1)}
	m.Update(doMsg(func(m *screenModel) tea.Cmd { return m.open(p) }))
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if r := <-p.reply; r.value != "approve" {
		t.Fatalf("a menu the user opened: %v", r)
	}
}
