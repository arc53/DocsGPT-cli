package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Item is one choice of a Select.
type Item struct {
	Label       string
	Description string   // dim second column (list) or line under the row (inline)
	Value       string   // returned by Run; Label when empty
	Keys        []string // pick the item directly ("a", "1", "ctrl+y"); the first is shown
}

func (it Item) key() string {
	if len(it.Keys) == 0 {
		return ""
	}
	return it.Keys[0]
}

func (it Item) value() string {
	if it.Value != "" {
		return it.Value
	}
	return it.Label
}

// Select asks the user to pick one item: a vertical list with an optional
// type-to-filter line, or with Inline a one-line row of choices.
type Select struct {
	Title   string
	Items   []Item
	Default int  // initially selected item
	Filter  bool // type to filter; printable item keys are then ignored
	Inline  bool // one row, ←/→ to move (falls back to a list when too wide)
	Height  int  // visible rows of a list (default 8)
	Stderr  bool // draw on stderr, leaving stdout to the answer
	// Summary is the line left once an item is chosen; it defaults to
	// "Title: Label" and an empty result leaves nothing.
	Summary func(Item) string
}

// Run shows the prompt and returns the chosen item's value.
func (s Select) Run() (string, error) {
	if len(s.Items) == 0 {
		return "", errors.New("ui: select has no items")
	}
	m, err := run(newSelectModel(s), s.Stderr)
	if err != nil {
		return "", err
	}
	sm := m.(*selectModel)
	if sm.chosen == nil {
		return "", ErrCancelled
	}
	return sm.chosen.value(), nil
}

type selectModel struct {
	Select
	filter  field
	matches []int // indices into Items, best first
	cursor  int   // index into matches
	width   int
	maxRows int
	done    bool
	chosen  *Item
}

func newSelectModel(s Select) *selectModel {
	if s.Height <= 0 {
		s.Height = 8
	}
	m := &selectModel{Select: s, width: 80, maxRows: s.Height}
	m.matches = filterItems(s.Items, "")
	m.cursor = clamp(s.Default, 0, len(s.Items)-1)
	return m
}

func (m *selectModel) setSize(w, h int) {
	m.width = w
	// Title, filter, scroll info and hint lines stay on screen.
	m.maxRows = clamp(h-5, 1, m.Height)
}

func (m *selectModel) Init() tea.Cmd { return nil }

func (m *selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.setSize(msg.Width, msg.Height)
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *selectModel) key(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "ctrl+c":
		return m.finish(nil)
	case "esc":
		if m.Filter && len(m.filter.text) > 0 {
			m.setFilter("")
			return nil
		}
		return m.finish(nil)
	case "enter":
		if len(m.matches) > 0 {
			return m.finish(&m.Items[m.matches[m.cursor]])
		}
		return nil
	}
	if it := m.shortcut(k); it != nil {
		return m.finish(it)
	}
	if !m.move(k.String()) && m.Filter && m.filter.update(k) {
		m.setFilter(m.filter.String())
	}
	return nil
}

// move applies a navigation key and reports whether s was one.
func (m *selectModel) move(s string) bool {
	n, inline := len(m.matches), m.inline()
	if n == 0 {
		return false
	}
	switch {
	case s == "up" || s == "ctrl+p" || s == "shift+tab" || inline && s == "left" || !m.Filter && s == "k":
		m.cursor = (m.cursor - 1 + n) % n
	case s == "down" || s == "ctrl+n" || s == "tab" || inline && s == "right" || !m.Filter && s == "j":
		m.cursor = (m.cursor + 1) % n
	case s == "home" && !m.Filter, s == "pgup" && m.cursor < m.maxRows:
		m.cursor = 0
	case s == "end" && !m.Filter, s == "pgdown" && m.cursor >= n-m.maxRows:
		m.cursor = n - 1
	case s == "pgup":
		m.cursor -= m.maxRows
	case s == "pgdown":
		m.cursor += m.maxRows
	default:
		return false
	}
	return true
}

// shortcut returns the item bound to k, if any.
func (m *selectModel) shortcut(k tea.KeyMsg) *Item {
	printable := k.Type == tea.KeyRunes || k.Type == tea.KeySpace
	if m.Filter && printable || k.Paste {
		return nil
	}
	for i := range m.Items {
		if slices.Contains(m.Items[i].Keys, k.String()) {
			return &m.Items[i]
		}
	}
	return nil
}

func (m *selectModel) setFilter(q string) {
	m.filter.set(q)
	m.matches = filterItems(m.Items, q)
	m.cursor = 0
}

func (m *selectModel) finish(it *Item) tea.Cmd {
	m.done, m.chosen = true, it
	return tea.Quit
}

// inline reports whether the inline row fits; otherwise it degrades to a list.
func (m *selectModel) inline() bool {
	return m.Inline && !m.Filter && lipgloss.Width(m.row()) <= m.width
}

func (m *selectModel) View() string {
	if m.done {
		if m.chosen == nil {
			return summary("")
		}
		if m.Summary != nil {
			return summary(m.Summary(*m.chosen))
		}
		return summary(answered(m.Title, m.chosen.Label))
	}
	var lines []string
	if m.inline() {
		row := m.row()
		if m.Title != "" {
			if t := bold(m.Title) + "  " + row; lipgloss.Width(t) <= m.width {
				row = t
			} else {
				lines = append(lines, bold(m.Title))
			}
		}
		lines = append(lines, row)
		if desc := m.Items[m.matches[m.cursor]].Description; desc != "" {
			lines = append(lines, fg(Colors.Muted).Render(desc))
		}
		lines = append(lines, hints(m.width, "←→", "choose", "enter", "confirm", "esc", "cancel"))
		return frame(lines, m.width)
	}

	if m.Title != "" {
		lines = append(lines, bold(m.Title))
	}
	if m.Filter {
		lines = append(lines, fg(Colors.Dim).Render("> ")+m.filter.view(m.width-2, false, "type to filter"))
	}
	lines = append(lines, m.list()...)
	esc := "cancel"
	if m.Filter && len(m.filter.text) > 0 {
		esc = "clear"
	}
	lines = append(lines, hints(m.width, "↑↓", "navigate", "enter", "select", "esc", esc))
	return frame(lines, m.width)
}

// row renders the inline choices, the selected one behind the arrow.
func (m *selectModel) row() string {
	parts := make([]string, len(m.Items))
	for i, it := range m.Items {
		label := accelerate(it.Label, it.key())
		if i == m.cursor {
			parts[i] = fg(Colors.Accent).Render("→ " + label)
		} else {
			parts[i] = "  " + label
		}
	}
	return strings.Join(parts, "  ")
}

// accelerate underlines the first letter of label that is its shortcut key.
func accelerate(label, key string) string {
	if len([]rune(key)) != 1 {
		return label
	}
	i := strings.Index(label, key)
	if i < 0 {
		i = strings.Index(strings.ToLower(label), strings.ToLower(key))
	}
	if i < 0 {
		return label + " " + fg(Colors.Dim).Render("("+key+")")
	}
	n := len(string([]rune(label[i:])[0]))
	return label[:i] + underline(label[i:i+n]) + label[i+n:]
}

// list renders the visible window of matches with an aligned description
// column and a (n/total) indicator when the list scrolls.
func (m *selectModel) list() []string {
	n := len(m.matches)
	if n == 0 {
		return []string{fg(Colors.Muted).Render("  No matches")}
	}
	m.cursor = clamp(m.cursor, 0, n-1)
	rows := min(m.maxRows, n)
	start := clamp(m.cursor-rows/2, 0, n-rows)

	keyed, described := false, false
	labelW := 0
	for _, i := range m.matches {
		labelW = max(labelW, lipgloss.Width(m.Items[i].Label))
		keyed = keyed || m.Items[i].key() != "" && !m.Filter
		described = described || m.Items[i].Description != ""
	}
	keyW := 0
	if keyed {
		for _, it := range m.Items {
			keyW = max(keyW, lipgloss.Width(it.key())+1)
		}
	}
	avail := m.width - 2 - keyW
	if described {
		// Leave the descriptions at least half of a narrow terminal.
		labelW = min(labelW, 32, max(avail/2, avail-40))
	}
	labelW = clamp(labelW, 1, avail)
	descW := avail - labelW - 2

	out := make([]string, 0, rows+1)
	for c := start; c < start+rows; c++ {
		it := m.Items[m.matches[c]]
		sel := c == m.cursor
		key := ""
		if keyed {
			key = fmt.Sprintf("%-*s", keyW, it.key())
			if !sel {
				key = fg(Colors.Dim).Render(key)
			}
		}
		label := ansi.Truncate(it.Label, labelW, "…")
		line := label
		if it.Description != "" && descW >= 10 {
			desc := strings.Repeat(" ", labelW-lipgloss.Width(label)+2) + ansi.Truncate(it.Description, descW, "…")
			if !sel {
				desc = fg(Colors.Muted).Render(desc)
			}
			line += desc
		}
		if sel {
			out = append(out, fg(Colors.Accent).Render("→ "+key+line))
		} else {
			out = append(out, "  "+key+line)
		}
	}
	if rows < n {
		out = append(out, fg(Colors.Muted).Render(fmt.Sprintf("  (%d/%d)", m.cursor+1, n)))
	}
	return out
}
