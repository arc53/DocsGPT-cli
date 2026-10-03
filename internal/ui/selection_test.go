package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func mpress(m *screenModel, x, y int) {
	m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
}

func drag(m *screenModel, x, y int) tea.Cmd {
	_, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	return cmd
}

func release(m *screenModel, x, y int) tea.Cmd {
	_, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	return cmd
}

// transcript is the transcript's rows of the frame, styled.
func transcript(m *screenModel) string {
	return strings.Join(strings.Split(m.View(), "\n")[:m.viewRows], "\n")
}

// TestScreenSelect: a drag selects and highlights cells, the release
// copies them and says so; a click clears the selection, as does Esc.
func TestScreenSelect(t *testing.T) {
	copied := stubClipboard(t)
	a := &para{rows: []string{"first paragraph", "  wraps here", "second"}, plain: []Plain{{}, wrapped(2, " "), {}}}
	m := testScreen(40, 12, a, &numbered{"b", 2})
	rows(m)
	mpress(m, 6, 0)
	drag(m, 5, 1)
	if v := m.View(); !strings.Contains(v, "first \x1b[7mparagraph\x1b[27m") || !strings.Contains(v, "\x1b[7m  wrap\x1b[27ms here") {
		t.Fatalf("highlight:\n%q", v)
	}
	cmd := release(m, 5, 1)
	if cmd == nil {
		t.Fatal("nothing copied")
	}
	m.Update(cmd())
	if len(*copied) != 1 || (*copied)[0] != "paragraph wrap" {
		t.Fatalf("copied %q", *copied)
	}
	if r := rows(m); !strings.Contains(r[7], "Copied 14 characters") || m.sel == nil {
		t.Fatalf("status %q, selection %v", r[7], m.sel)
	}

	m.click.at = time.Time{}
	mpress(m, 2, 3) // a click elsewhere
	if release(m, 2, 3) != nil || m.sel != nil || strings.Contains(transcript(m), "\x1b[7m") {
		t.Fatal("a click did not just clear the selection")
	}

	m.click.at = time.Time{}
	mpress(m, 0, 0)
	drag(m, 30, 4)
	if c := release(m, 30, 4); c != nil {
		m.Update(c())
	}
	if got := (*copied)[1]; got != "first paragraph wraps here\nsecond\n\nb 0" {
		t.Fatalf("across blocks: %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.sel != nil {
		t.Fatal("esc kept the selection")
	}
	cancelled := false
	m.cancel = func() { cancelled = true }
	m.sel = &selection{to: point{0, 3}}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cancelled || m.sel != nil {
		t.Fatal("esc stopped the answer before it cleared the selection")
	}
	m.sel = &selection{to: point{0, 3}}
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	if m.sel != nil {
		t.Fatal("a resize kept the selection")
	}
}

// TestScreenClicks: two clicks select a word, three the line as it was
// before it was wrapped; both copy at once.
func TestScreenClicks(t *testing.T) {
	copied := stubClipboard(t)
	a := &para{rows: []string{"see /usr/local/bin-x and e.g. foo.", "  the end", "next"}, plain: []Plain{{}, wrapped(2, " "), {}}}
	m := testScreen(50, 12, a)
	rows(m)
	click := func(x, y int) {
		mpress(m, x, y)
		if c := release(m, x, y); c != nil {
			m.Update(c())
		}
	}
	for _, c := range []struct {
		x    int
		want string
	}{{8, "/usr/local/bin-x"}, {27, "e.g"}, {32, "foo"}} {
		m.click.at = time.Time{}
		click(c.x, 0)
		click(c.x, 0)
		if got := (*copied)[len(*copied)-1]; got != c.want {
			t.Errorf("double click at %d: %q, want %q", c.x, got, c.want)
		}
	}
	click(32, 0)
	if got := (*copied)[len(*copied)-1]; got != "see /usr/local/bin-x and e.g. foo. the end" {
		t.Errorf("triple click: %q", got)
	}
	if !strings.Contains(m.View(), "\x1b[7m  the end") {
		t.Errorf("triple click highlight:\n%s", m.View())
	}
}

// TestScreenSelectScrolls: dragging on the top row scrolls up while held;
// the selection stays on its text while lines come in.
func TestScreenSelectScrolls(t *testing.T) {
	stubClipboard(t)
	answer := &numbered{"line", 30}
	m := testScreen(40, 12, answer) // 7 rows, lines 23-29 shown
	rows(m)
	mpress(m, 0, 0)
	if drag(m, 5, 0) != nil {
		t.Fatal("a drag along the top row scrolled")
	}
	release(m, 5, 0)
	mpress(m, 0, 3) // line 26
	cmd := drag(m, 3, 0)
	if cmd == nil || m.sel.dir != -1 {
		t.Fatal("no scrolling on the top row")
	}
	for range 3 {
		m.Update(dragTickMsg{})
	}
	rows(m)
	if m.top != 20 || m.sel.from != (point{20, 3}) || m.sel.to != (point{26, 1}) {
		t.Fatalf("top %d, selection %v-%v", m.top, m.sel.from, m.sel.to)
	}
	release(m, 3, 0)
	if _, cmd := m.Update(dragTickMsg{}); cmd != nil || m.top != 20 {
		t.Fatal("scrolled after the release")
	}
	answer.n = 40
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlEnd})
	if got := m.selected(); got != "e 20\nline 21\nline 22\nline 23\nline 24\nline 25\nl" || strings.Contains(transcript(m), "\x1b[7m") {
		t.Fatalf("after the answer grew: %q", got)
	}
}

// TestHighlight: reverse video over the selected columns, set again after
// the line's own styles, wide characters taken whole.
func TestHighlight(t *testing.T) {
	line := "ab\x1b[1m中文\x1b[0mcd   "
	got := highlight(line, 3, 7)
	if ansi.Strip(got) != ansi.Strip(line) || !strings.Contains(got, "\x1b[7m") || !strings.Contains(got, "\x1b[0m\x1b[7mc") {
		t.Fatalf("%q", got)
	}
	if plain := ansi.Strip(ansi.Cut(got, 0, 2)); plain != "ab" {
		t.Fatalf("before: %q", plain)
	}
	if highlight("abc   ", 4, 6) != "abc   " {
		t.Fatal("highlighted the padding")
	}
}
