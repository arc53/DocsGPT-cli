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

// TestScreenLinks: a click on a hyperlink opens it (not over SSH); a drag
// over one copies its text.
func TestScreenLinks(t *testing.T) {
	copied := stubClipboard(t)
	for _, k := range []string{"SSH_TTY", "SSH_CONNECTION", "SSH_CLIENT"} {
		t.Setenv(k, "")
	}
	var opened []string
	open := openURL
	openURL = func(u string) error { opened = append(opened, u); return nil }
	t.Cleanup(func() { openURL = open })

	link := "see \x1b[4m\x1b]8;;https://a.example/x\x1b\\the 中文 docs\x1b]8;;\x1b\\\x1b[0m now"
	a := &para{rows: []string{link, "next"}}
	m := testScreen(50, 12, a)
	rows(m)
	click := func(x, y int) {
		m.click.at = time.Time{}
		mpress(m, x, y)
		if c := release(m, x, y); c != nil {
			m.Update(c())
		}
	}
	for x, want := range map[int]string{1: "", 4: "https://a.example/x", 10: "https://a.example/x", 15: "https://a.example/x", 17: "", 30: ""} {
		opened = nil
		click(x, 0)
		if got := strings.Join(opened, " "); got != want {
			t.Errorf("click at %d opened %q, want %q", x, got, want)
		}
	}
	click(6, 0)
	if r := rows(m); !strings.Contains(r[m.viewRows], "Opened https://a.example/x") {
		t.Errorf("status: %q", r[m.viewRows])
	}
	if len(*copied) != 0 {
		t.Errorf("a click copied %q", *copied)
	}

	m.click.at = time.Time{}
	mpress(m, 0, 0)
	drag(m, 20, 0)
	m.Update(release(m, 20, 0)())
	if got := (*copied)[0]; got != "see the 中文 docs now" {
		t.Errorf("copied %q", got)
	}

	t.Setenv("SSH_TTY", "/dev/pts/1")
	opened = nil
	click(6, 0)
	if len(opened) != 0 {
		t.Errorf("opened over SSH: %q", opened)
	}
}

// TestScreenClickToggles: a click on a tool block that folds expands or
// collapses it alone, once no second click came; its first line stays on
// its row. A click elsewhere, a double click or a drag toggles nothing.
func TestScreenClickToggles(t *testing.T) {
	stubClipboard(t)
	f1, f2 := &fold{name: "f1", n: 20}, &fold{name: "f2", n: 2}
	m := testScreen(70, 12, &numbered{"a", 2}, f1, f2, &numbered{"b", 2}) // 7 transcript rows
	m.top, m.follow = 0, false
	rows(m) // a 0, a 1, "", $ f1, f1 out 17 …
	click := func(x, y int) tea.Cmd {
		m.click.at = time.Time{}
		mpress(m, x, y)
		return release(m, x, y)
	}

	cmd := click(3, 4) // f1 out 17
	if cmd == nil {
		t.Fatal("a click on a block that folds did nothing")
	}
	if f1.expanded {
		t.Fatal("toggled before a second click could come")
	}
	m.Update(cmd())
	r := rows(m)
	if !f1.expanded || f2.expanded || r[3] != "$ f1" || r[4] != "f1 out 0" || !strings.Contains(r[7], "Tool output expanded") {
		t.Fatalf("expanded: %v %v %q / %q", f1.expanded, f2.expanded, r[:7], r[7])
	}
	// (As the tick of a click would, without the wait.)
	toggled := func(cmd tea.Cmd, b Block) {
		t.Helper()
		if cmd == nil {
			t.Fatal("no toggle pending")
		}
		m.Update(toggleMsg{m.click.toggle, b})
	}
	toggled(click(3, 3), f1)
	if r = rows(m); f1.expanded || r[3] != "$ f1" || !strings.Contains(r[7], "Tool output collapsed") {
		t.Fatalf("collapsed: %q / %q", r[:7], r[7])
	}

	// Started above the view: its first line comes to the top.
	toggled(click(3, 3), f1)
	m.top = m.offset(1) + 5
	rows(m)
	m.Update(toggleMsg{m.click.toggle, f1})
	if r = rows(m); f1.expanded || r[0] != "$ f1" {
		t.Fatalf("collapsed from below its start: %q", r[:3])
	}

	// A second click (a double click) cancels the first's toggle.
	m.click.at = time.Time{}
	mpress(m, 3, 1)
	if release(m, 3, 1) == nil {
		t.Fatal("no toggle pending")
	}
	first := m.click.toggle
	mpress(m, 3, 1)
	release(m, 3, 1)
	m.Update(toggleMsg{first, f1})
	if f1.expanded {
		t.Fatal("a double click toggled the block")
	}

	// A block that does not fold, another block, the blank between them.
	m.top = m.offset(2)
	rows(m) // $ f2, f2 out 0, f2 out 1, "", b 0 …
	for _, y := range []int{0, 1, 3, 4} {
		if click(3, y) != nil {
			t.Fatalf("a click on row %d would toggle", y)
		}
	}
}
