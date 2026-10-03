package ui

import (
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// Selecting text with the mouse, as in pi: a press in the transcript starts
// a selection, a drag extends it (scrolling while the pointer is under the
// transcript, or on its top row come from below), and the release copies
// it. Two clicks select a word, three a line as it was before it was
// wrapped (a paragraph). The selection holds transcript lines, not screen
// rows, so it stays on its text while the transcript scrolls or grows; the
// next click, Esc or a resize clears it.

const (
	multiClick = 500 * time.Millisecond // between the clicks of a double or triple click
	dragScroll = 50 * time.Millisecond  // a line scrolled while dragging outside
)

type point struct{ line, col int }

func (p point) before(q point) bool { return p.line < q.line || p.line == q.line && p.col < q.col }

type selection struct {
	from, to      point    // the selected cells: from on, up to before to
	anchor        point    // the cell pressed (by cells)
	unit          int      // 1 cells, 2 words, 3 lines
	first         [2]point // the word or line clicked (by words, lines)
	held, dragged bool
	x, y          int // the pointer, while held
	dir           int // the drag scrolls up (-1) or down (1)
	ticking       bool
}

func (s *selection) empty() bool { return s.from == s.to }

type (
	dragTickMsg struct{}
	copiedMsg   struct {
		n   int
		err error
	}
	flashMsg struct{ id int }
)

// pointer handles a mouse event other than the wheel.
func (m *screenModel) pointer(msg tea.MouseMsg) tea.Cmd {
	s := m.sel
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		return m.press(msg.X, msg.Y)
	case s == nil || !s.held:
	case msg.Action == tea.MouseActionMotion:
		// The top row scrolls up once the pointer comes to it from below,
		// not while a selection on it is dragged along it.
		up := msg.Y <= 0 && (s.y > 0 || s.dir < 0)
		s.x, s.y, s.dragged = msg.X, msg.Y, true
		m.extend()
		s.dir = 0
		if up {
			s.dir = -1
		} else if msg.Y >= m.viewRows {
			s.dir = 1
		}
		if s.dir != 0 && !s.ticking {
			s.ticking = true
			return dragTick()
		}
	case msg.Action == tea.MouseActionRelease:
		s.held, s.dir = false, 0
		if s.unit == 1 && !s.dragged {
			m.sel = nil // a click
			return nil
		}
		s.x, s.y = msg.X, msg.Y
		m.extend()
		if text := m.selected(); text != "" {
			return m.copy(text)
		}
	}
	return nil
}

func dragTick() tea.Cmd {
	return tea.Tick(dragScroll, func(time.Time) tea.Msg { return dragTickMsg{} })
}

// dragged scrolls a line while the pointer is held outside the transcript.
func (m *screenModel) dragged() tea.Cmd {
	s := m.sel
	if s == nil || !s.held || s.dir == 0 {
		if s != nil {
			s.ticking = false
		}
		return nil
	}
	top, follow := m.top, m.follow
	m.scroll(s.dir)
	if m.top == top && m.follow == follow {
		s.ticking = false
		return nil
	}
	m.extend()
	return dragTick()
}

// press starts a selection at the cell pressed, a word or a line on a
// double or triple click. A press outside the transcript clears it.
func (m *screenModel) press(x, y int) tea.Cmd {
	if y >= m.viewRows || m.total == 0 {
		m.sel = nil
		return nil
	}
	p := m.at(x, y)
	word := m.word(p)
	n := 1
	if time.Since(m.click.at) < multiClick && m.click.word == word {
		n = m.click.n%3 + 1
	}
	m.click.at, m.click.word, m.click.n = time.Now(), word, n
	s := &selection{anchor: p, from: p, to: p, unit: n, held: true, x: x, y: y}
	switch n {
	case 2:
		s.first = word
	case 3:
		s.first = m.logical(p)
	}
	if n > 1 {
		s.from, s.to = s.first[0], s.first[1]
	}
	m.sel = s
	return nil
}

// extend moves the selection's end to the pointer, by its unit.
func (m *screenModel) extend() {
	s := m.sel
	p := m.at(s.x, s.y)
	if s.unit == 1 {
		a, b := s.anchor, p
		if b.before(a) {
			a, b = b, a
		}
		s.from, s.to = a, point{b.line, b.col + 1}
		if a == b && !s.dragged {
			s.to = a
		}
		return
	}
	r := m.word(p)
	if s.unit == 3 {
		r = m.logical(p)
	}
	if r[0].before(s.first[0]) {
		s.from, s.to = r[0], s.first[1]
	} else {
		s.from, s.to = s.first[0], r[1]
	}
}

// at is the transcript cell at screen cell x, y, as last drawn; past the
// transcript's end, its last line's end.
func (m *screenModel) at(x, y int) point {
	line := m.top + clamp(y, 0, max(0, m.viewRows-1))
	if line >= m.total {
		return point{max(0, m.total-1), endCol}
	}
	return point{line, max(0, x)}
}

// word is the word at p: letters, digits and _, with the / and - between
// and around them and the . and ' inside them (a path, a URL, kebab-case,
// e.g.); else the run of spaces or the one character at p.
func (m *screenModel) word(p point) [2]point {
	lines, _ := m.rows(p.line, p.line, false)
	if len(lines) == 0 {
		return [2]point{p, p}
	}
	type cell struct {
		r          rune
		start, end int
	}
	var cs []cell
	col := 0
	g := uniseg.NewGraphemes(ansi.Strip(lines[0]))
	for g.Next() {
		r, _ := utf8.DecodeRuneInString(g.Str())
		cs = append(cs, cell{r, col, col + g.Width()})
		col += g.Width()
	}
	letter := func(i int) bool {
		if i < 0 || i >= len(cs) {
			return false
		}
		r := cs[i].r
		return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_'
	}
	kind := func(i int) int { // 0 other, 1 space, 2 part of a word
		switch {
		case i < 0 || i >= len(cs):
			return -1
		case cs[i].r == ' ':
			return 1
		case letter(i), cs[i].r == '/' || cs[i].r == '-',
			(cs[i].r == '.' || cs[i].r == '\'') && letter(i-1) && letter(i+1):
			return 2
		}
		return 0
	}
	at := -1
	for i, c := range cs {
		if p.col >= c.start && p.col < c.end {
			at = i
		}
	}
	if at < 0 {
		return [2]point{{p.line, col}, {p.line, col}}
	}
	a, b := at, at
	if k := kind(at); k > 0 {
		for kind(a-1) == k {
			a--
		}
		for kind(b+1) == k {
			b++
		}
	}
	return [2]point{{p.line, cs[a].start}, {p.line, cs[b].end}}
}

// logical is the line at p as it was before it was wrapped: the lines of
// its block that wrap on from one another.
func (m *screenModel) logical(p point) [2]point {
	a, b := p.line, p.line
	lines, plain := m.rows(max(0, p.line-200), p.line+200, true)
	i := p.line - max(0, p.line-200)
	if i >= len(lines) {
		return [2]point{{a, 0}, {b, endCol}}
	}
	for k := i; k > 0 && plain[k].Wrap; k-- {
		a--
	}
	for k := i + 1; k < len(plain) && plain[k].Wrap; k++ {
		b++
	}
	return [2]point{{a, 0}, {b, endCol}}
}

// rows returns the transcript's lines a to b, and how they copy when
// withPlain.
func (m *screenModel) rows(a, b int, withPlain bool) (lines []string, plain []Plain) {
	at := 0
	for i, blk := range m.spanBlocks() {
		l := blk.Lines(m.width)
		if i > 0 {
			if at >= a && at <= b {
				lines, plain = append(lines, ""), append(plain, Plain{})
			}
			at++
		}
		if at <= b && at+len(l) > a {
			var p []Plain
			if c, ok := blk.(Plainer); ok && withPlain {
				p = c.Plain(m.width)
			}
			for k := max(0, a-at); k < len(l) && at+k <= b; k++ {
				lines = append(lines, l[k])
				if k < len(p) {
					plain = append(plain, p[k])
				} else {
					plain = append(plain, Plain{})
				}
			}
		}
		at += len(l)
		if at > b {
			break
		}
	}
	return lines, plain
}

// spanBlocks returns the blocks that show something.
func (m *screenModel) spanBlocks() []Block {
	var out []Block
	for _, b := range m.blocks {
		if len(b.Lines(m.width)) > 0 {
			out = append(out, b)
		}
	}
	return out
}

// selected returns the selected text (see Unwrap).
func (m *screenModel) selected() string {
	s := m.sel
	if s == nil || s.empty() {
		return ""
	}
	lines, plain := m.rows(s.from.line, s.to.line, true)
	return Unwrap(lines, plain, s.from.col, s.to.col)
}

// align widens columns a to b of plain to whole characters.
func align(plain string, a, b int) (int, int) {
	col := 0
	g := uniseg.NewGraphemes(plain)
	for g.Next() && col < b {
		w := g.Width()
		if col < a && col+w > a {
			a = col
		}
		if col < b && col+w > b {
			b = col + w
		}
		col += w
	}
	return a, b
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// highlight shows columns a to b of a styled line in reverse video, which
// reads on any background and without colours. Every style change inside
// sets reverse again.
func highlight(line string, a, b int) string {
	plain := ansi.Strip(line)
	b = min(b, ansi.StringWidth(strings.TrimRight(plain, " ")))
	if a, b = align(plain, a, b); a >= b {
		return line
	}
	mid := sgr.ReplaceAllStringFunc(ansi.Cut(line, a, b), func(s string) string { return s + "\x1b[7m" })
	return ansi.Cut(line, 0, a) + "\x1b[7m" + mid + "\x1b[27m" + ansi.TruncateLeft(line, b, "")
}

// copy copies text and says so in the status row.
func (m *screenModel) copy(text string) tea.Cmd {
	out := m.out
	return func() tea.Msg {
		var w io.Writer = io.Discard
		if out != nil {
			w = out
		}
		return copiedMsg{n: utf8.RuneCountInString(text), err: Copy(text, w)}
	}
}
