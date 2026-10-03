package ui

import (
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Command is a slash command the editor offers once the input starts with "/".
type Command struct {
	Name        string // without the slash
	Description string
}

// PasteMarker matches the marker a collapsed paste leaves in the editor.
var PasteMarker = regexp.MustCompile(`\[paste #(\d+) (?:\+\d+ lines|\d+ chars)\]`)

// quitWindow is how soon a second Ctrl+C on an empty editor quits.
const quitWindow = time.Second

// undoMax is how many steps back Ctrl+- goes.
const undoMax = 100

// editorModel is the chat input of a Screen: multi-line text between two
// dim rules, a dim footer line under them, prompt history on ↑/↓, large
// pastes collapsed to a marker, and a popup of the slash commands.
type editorModel struct {
	commands []Command
	history  *History
	footer   string // left of the footer line
	status   string // right of the footer line

	lines    [][]rune
	row, col int // cursor: line, rune
	goal     int // visual column ↑/↓ keep, -1 when unset
	pastes   map[int]string
	hist     int    // history entry shown; len(entries) for the draft
	draft    string // the text before browsing the history
	popup    *selectModel
	shut     string // text the popup was dismissed on (Esc)
	top      int    // first visual row shown
	quitAt   time.Time
	width    int
	height   int
	undo     []state // the states before the edits, the last one last
	last     string  // the kind of the last edit (see editKind), "" after a move
}

// state is what an undo goes back to.
type state struct {
	lines    [][]rune
	row, col int
	pastes   map[int]string
}

// editorAction is what a key asks of the Screen.
type editorAction int

const (
	editNothing editorAction = iota
	editSubmit
	editQuit
)

type (
	hintMsg   struct{}
	editedMsg struct {
		path string
		err  error
	}
)

func newEditorModel(commands []Command, history *History) *editorModel {
	if history == nil {
		history = LoadHistory("")
	}
	return &editorModel{
		commands: commands, history: history, lines: [][]rune{{}}, goal: -1, pastes: map[int]string{},
		hist: len(history.entries), width: 80, height: 24,
	}
}

func (m *editorModel) setSize(w, h int) { m.width, m.height = max(w, 10), max(h, 5) }

func (m *editorModel) text() string { return textOf(m.lines) }

func textOf(lines [][]rune) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n")
}

func (m *editorModel) expanded() string {
	return PasteMarker.ReplaceAllStringFunc(m.text(), func(marker string) string {
		id, _ := strconv.Atoi(PasteMarker.FindStringSubmatch(marker)[1])
		if p, ok := m.pastes[id]; ok {
			return p
		}
		return marker
	})
}

func (m *editorModel) empty() bool { return len(m.lines) == 1 && len(m.lines[0]) == 0 }

// setText replaces the text, the cursor at its end.
func (m *editorModel) setText(s string) {
	s = printable(s) // history and $EDITOR text are not typed
	m.lines = nil
	for _, l := range strings.Split(s, "\n") {
		m.lines = append(m.lines, []rune(l))
	}
	m.row = len(m.lines) - 1
	m.col = len(m.lines[m.row])
}

// update handles a message for the editor and says what it asks of the
// Screen.
func (m *editorModel) update(msg tea.Msg) (tea.Cmd, editorAction) {
	switch msg := msg.(type) {
	case hintMsg:
		if time.Since(m.quitAt) >= quitWindow {
			m.quitAt = time.Time{}
		}
	case editedMsg:
		if data, err := os.ReadFile(msg.path); err == nil && msg.err == nil {
			m.replace(strings.TrimRight(string(data), "\n"), nil)
		}
		os.Remove(msg.path)
	case tea.KeyMsg:
		if msg.String() == "ctrl+_" { // Ctrl+-
			m.undoLast()
			return nil, editNothing
		}
		before, row, col := m.save(), m.row, m.col
		browsing := m.hist < len(m.history.entries)
		cmd, act := m.key(msg)
		m.snap(row, col)
		if m.text() == textOf(before.lines) {
			m.last = ""
			return cmd, act
		}
		m.record(before, editKind(msg), browsing)
		if s := msg.String(); s != "up" && s != "down" {
			m.changed()
		}
		return cmd, act
	}
	return nil, editNothing
}

// editKind names the edit a key made: a word typed, a space typed, a
// paste, or the key.
func editKind(k tea.KeyMsg) string {
	switch {
	case k.Paste:
		return "paste"
	case (k.Type == tea.KeyRunes || k.Type == tea.KeySpace) && !k.Alt:
		if strings.ContainsFunc(string(k.Runes), unicode.IsSpace) {
			return "space"
		}
		return "word"
	}
	return k.String()
}

// record keeps the state before an edit of kind for undo. As in pi (and
// fish), a word typed is one step together with the space before it, and
// so is a run of the same deleting key; a recall from the history is one
// step back to the draft, however far ↑ went.
func (m *editorModel) record(before state, kind string, browsing bool) {
	push := true
	switch kind {
	case "word":
		push = m.last != "word" && m.last != "space"
	case "backspace", "ctrl+h", "delete", "ctrl+d":
		push = m.last != kind
	case "up", "down":
		push = !browsing
	}
	m.last = kind
	if !push {
		return
	}
	m.undo = append(m.undo, before)
	if len(m.undo) > undoMax {
		m.undo = slices.Delete(m.undo, 0, len(m.undo)-undoMax)
	}
}

// save copies the state for undo.
func (m *editorModel) save() state {
	lines := make([][]rune, len(m.lines))
	for i, l := range m.lines {
		lines[i] = slices.Clone(l)
	}
	return state{lines, m.row, m.col, maps.Clone(m.pastes)}
}

// undoLast goes back to the state before the last edit.
func (m *editorModel) undoLast() {
	if len(m.undo) == 0 {
		return
	}
	s := m.undo[len(m.undo)-1]
	m.undo = m.undo[:len(m.undo)-1]
	m.lines, m.row, m.col, m.pastes = s.lines, s.row, s.col, s.pastes
	m.last, m.goal, m.shut = "", -1, ""
	m.changed()
}

// replace sets the text and its pastes as one edit undo can take back
// (the $EDITOR result, queued messages put back).
func (m *editorModel) replace(text string, pastes map[int]string) {
	if before := m.save(); text != m.text() || !maps.Equal(pastes, m.pastes) {
		m.record(before, "replace", false)
	}
	m.setText(text)
	m.pastes = maps.Clone(pastes)
	if m.pastes == nil {
		m.pastes = map[int]string{}
	}
	m.last = ""
	m.changed()
}

// changed follows an edit: history browsing ends, pastes whose marker is
// gone are dropped and the popup refreshes.
func (m *editorModel) changed() {
	m.hist = len(m.history.entries)
	kept := map[int]string{}
	for _, sub := range PasteMarker.FindAllStringSubmatch(m.text(), -1) {
		if id, _ := strconv.Atoi(sub[1]); m.pastes[id] != "" {
			kept[id] = m.pastes[id]
		}
	}
	m.pastes = kept
	m.refreshPopup()
}

func (m *editorModel) key(k tea.KeyMsg) (tea.Cmd, editorAction) {
	s := k.String()
	if s != "ctrl+c" {
		m.quitAt = time.Time{}
	}
	if s != "up" && s != "down" {
		m.goal = -1
	}
	if k.Paste {
		m.paste(string(k.Runes))
		return nil, editNothing
	}
	if m.popup != nil {
		switch s {
		case "up", "ctrl+p", "down", "ctrl+n":
			m.popup.move(strings.TrimPrefix(s, "ctrl+"))
			return nil, editNothing
		case "tab":
			m.setText("/" + m.selected() + " ")
			m.popup = nil
			return nil, editNothing
		case "enter":
			if !m.isCommand(strings.TrimPrefix(m.text(), "/")) {
				m.setText("/" + m.selected())
			}
			return nil, editSubmit
		case "esc":
			m.popup, m.shut = nil, m.text()
			return nil, editNothing
		}
	}

	line := m.lines[m.row]
	switch s {
	case "ctrl+c":
		if !m.empty() {
			m.lines, m.row, m.col, m.pastes = [][]rune{{}}, 0, 0, map[int]string{}
			m.quitAt = time.Time{}
			return nil, editNothing
		}
		if !m.quitAt.IsZero() && time.Since(m.quitAt) < quitWindow {
			return nil, editQuit
		}
		m.quitAt = time.Now()
		return tea.Tick(quitWindow, func(time.Time) tea.Msg { return hintMsg{} }), editNothing
	case "ctrl+d":
		if m.empty() {
			return nil, editQuit
		}
		m.deleteForward()
	case "enter":
		if m.col > 0 && line[m.col-1] == '\\' {
			m.lines[m.row] = slices.Delete(line, m.col-1, m.col)
			m.col--
			m.insert("\n")
			return nil, editNothing
		}
		if strings.TrimSpace(m.text()) == "" {
			return nil, editNothing
		}
		return nil, editSubmit
	case "ctrl+j", "alt+enter":
		m.insert("\n")
	case "up":
		m.up()
	case "down":
		m.down()
	case "left", "ctrl+b":
		if m.col > 0 {
			m.col--
		} else if m.row > 0 {
			m.row--
			m.col = len(m.lines[m.row])
		}
	case "right", "ctrl+f":
		if m.col < len(line) {
			m.col++
		} else if m.row < len(m.lines)-1 {
			m.row, m.col = m.row+1, 0
		}
	case "alt+left", "alt+b", "ctrl+left":
		if m.col == 0 && m.row > 0 {
			m.row--
			m.col = len(m.lines[m.row])
		} else {
			m.col = wordStart(line, m.col)
		}
	case "alt+right", "alt+f", "ctrl+right":
		if m.col == len(line) && m.row < len(m.lines)-1 {
			m.row, m.col = m.row+1, 0
		} else {
			m.col = wordEnd(line, m.col)
		}
	case "home", "ctrl+a":
		m.col = 0
	case "end", "ctrl+e":
		m.col = len(line)
	case "backspace", "ctrl+h":
		if m.col > 0 {
			m.cut(m.col-1, m.col)
		} else if m.row > 0 {
			prev := m.lines[m.row-1]
			m.col = len(prev)
			m.lines[m.row-1] = append(prev, line...)
			m.lines = slices.Delete(m.lines, m.row, m.row+1)
			m.row--
		}
	case "delete":
		m.deleteForward()
	case "ctrl+u":
		m.cut(0, m.col)
	case "ctrl+k":
		if m.col == len(line) {
			m.deleteForward()
		} else {
			m.cut(m.col, len(line))
		}
	case "ctrl+w", "alt+backspace":
		m.cut(wordStart(line, m.col), m.col)
	case "ctrl+g":
		return m.externalEditor(), editNothing
	default:
		if (k.Type == tea.KeyRunes || k.Type == tea.KeySpace) && !k.Alt {
			m.insert(strings.Map(func(r rune) rune {
				if unicode.IsPrint(r) {
					return r
				}
				return -1
			}, string(k.Runes)))
		}
	}
	return nil, editNothing
}

// take empties the editor for the next message and returns the one it
// held: the text with pastes expanded, and as shown (pastes as markers).
func (m *editorModel) take() (text, shown string) {
	text, shown = m.expanded(), m.text()
	m.history.Add(text)
	m.lines, m.row, m.col, m.pastes = [][]rune{{}}, 0, 0, map[int]string{}
	m.hist, m.draft, m.popup, m.shut, m.top, m.goal = len(m.history.entries), "", nil, "", 0, -1
	m.undo, m.last = nil, ""
	return text, shown
}

// insert puts s, which may hold newlines, at the cursor.
func (m *editorModel) insert(s string) {
	parts := strings.Split(s, "\n")
	line := m.lines[m.row]
	after := slices.Clone(line[m.col:])
	head := append(slices.Clone(line[:m.col]), []rune(parts[0])...)
	if len(parts) == 1 {
		m.lines[m.row] = append(head, after...)
		m.col = len(head)
		return
	}
	add := [][]rune{head}
	for _, p := range parts[1 : len(parts)-1] {
		add = append(add, []rune(p))
	}
	last := []rune(parts[len(parts)-1])
	add = append(add, append(last, after...))
	m.lines = slices.Replace(m.lines, m.row, m.row+1, add...)
	m.row += len(parts) - 1
	m.col = len(last)
}

// printable keeps the printable runes and line breaks of s, tabs as spaces:
// an escape sequence would be drawn raw by the editor.
func printable(s string) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", "    ").Replace(s)
	return strings.Map(func(r rune) rune {
		if r == '\n' || unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
}

// paste inserts pasted text; more than 10 lines or 1000 characters become
// a marker, expanded again when the message is sent (as pi does).
func (m *editorModel) paste(s string) {
	s = printable(s)
	n := strings.Count(s, "\n") + 1
	if n <= 10 && utf8.RuneCountInString(s) <= 1000 {
		m.insert(s)
		return
	}
	id := len(m.pastes) + 1
	for m.pastes[id] != "" {
		id++
	}
	m.pastes[id] = s
	if n > 10 {
		m.insert("[paste #" + strconv.Itoa(id) + " +" + strconv.Itoa(n) + " lines]")
	} else {
		m.insert("[paste #" + strconv.Itoa(id) + " " + strconv.Itoa(utf8.RuneCountInString(s)) + " chars]")
	}
}

func (m *editorModel) deleteForward() {
	line := m.lines[m.row]
	if m.col < len(line) {
		m.cut(m.col, m.col+1)
	} else if m.row < len(m.lines)-1 {
		m.lines[m.row] = append(line, m.lines[m.row+1]...)
		m.lines = slices.Delete(m.lines, m.row+1, m.row+2)
	}
}

// Paste markers are one unit: deleting part of one deletes all of it, and
// the cursor never rests inside one.

// cut deletes runes [start, end) of the current line, widened to the paste
// markers it reaches into, and leaves the cursor at the start.
func (m *editorModel) cut(start, end int) {
	for _, r := range m.markers() {
		if r[0] < end && start < r[1] {
			start, end = min(start, r[0]), max(end, r[1])
		}
	}
	m.lines[m.row] = slices.Delete(m.lines[m.row], start, end)
	m.col = start
}

// snap moves a cursor that landed inside a paste marker to its start or
// end, whichever lies the way it moved from (row, col).
func (m *editorModel) snap(row, col int) {
	for _, r := range m.markers() {
		if r[0] < m.col && m.col < r[1] {
			if m.row < row || m.row == row && m.col < col {
				m.col = r[0]
			} else {
				m.col = r[1]
			}
			return
		}
	}
}

// markers lists the rune ranges of the current line's paste markers.
func (m *editorModel) markers() [][2]int {
	return markerRanges(string(m.lines[m.row]), m.pastes)
}

func markerRanges(line string, pastes map[int]string) [][2]int {
	var out [][2]int
	for _, loc := range PasteMarker.FindAllStringSubmatchIndex(line, -1) {
		if id, _ := strconv.Atoi(line[loc[2]:loc[3]]); pastes[id] != "" {
			s := utf8.RuneCountInString(line[:loc[0]])
			out = append(out, [2]int{s, s + utf8.RuneCountInString(line[loc[0]:loc[1]])})
		}
	}
	return out
}

func wordStart(line []rune, i int) int {
	for i > 0 && unicode.IsSpace(line[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(line[i-1]) {
		i--
	}
	return i
}

func wordEnd(line []rune, i int) int {
	for i < len(line) && unicode.IsSpace(line[i]) {
		i++
	}
	for i < len(line) && !unicode.IsSpace(line[i]) {
		i++
	}
	return i
}

// History: ↑ on the first row of an empty editor, at its start, or while
// browsing shows the previous entry; ↓ on the last row the next one.
func (m *editorModel) up() {
	rows := m.layout()
	k := m.cursorRow(rows)
	if k > 0 {
		m.moveTo(rows, k-1)
		return
	}
	entries := m.history.entries
	browsing := m.hist < len(entries)
	if (m.empty() || browsing || m.col == 0) && m.hist > 0 {
		if !browsing {
			m.draft = m.expanded()
		}
		m.hist--
		m.setText(entries[m.hist])
		m.pastes = map[int]string{}
		m.refreshPopup()
		return
	}
	m.col = 0
}

func (m *editorModel) down() {
	rows := m.layout()
	k := m.cursorRow(rows)
	if k < len(rows)-1 {
		m.moveTo(rows, k+1)
		return
	}
	entries := m.history.entries
	if m.hist < len(entries) {
		m.hist++
		if m.hist == len(entries) {
			m.setText(m.draft)
		} else {
			m.setText(entries[m.hist])
		}
		m.refreshPopup()
		return
	}
	m.col = len(m.lines[m.row])
}

// vrow is one terminal row of the text: runes [start, end) of a line.
type vrow struct{ line, start, end int }

func runeWidth(r rune) int { return ansi.StringWidth(string(r)) }

// layout wraps the lines at the width less one column, kept for the
// cursor at the end of a row.
func (m *editorModel) layout() []vrow {
	w := max(1, m.width-1)
	var rows []vrow
	for i, l := range m.lines {
		for _, r := range wrapLine(l, w, markerRanges(string(l), m.pastes)) {
			rows = append(rows, vrow{i, r[0], r[1]})
		}
	}
	return rows
}

// wrapLine breaks a line into rows of at most w columns, as rune ranges:
// after the last space that fits, a word longer than a row where it
// reaches the edge. A paste marker (marks) is one unit. A space may hang
// into the cursor's column; when one ends the line, an empty row follows
// for the cursor after it.
func wrapLine(l []rune, w int, marks [][2]int) [][2]int {
	var rows [][2]int
	start, cols := 0, 0
	brk, brkCols := -1, 0 // after the last space: where the next row would start
	for i := 0; i < len(l); {
		j := i + 1
		for _, mk := range marks {
			if mk[0] == i && ansi.StringWidth(string(l[mk[0]:mk[1]])) <= w {
				j = mk[1]
			}
		}
		uw := ansi.StringWidth(string(l[i:j]))
		space := j == i+1 && unicode.IsSpace(l[i])
		if cols+uw > w && !(space && cols+uw <= w+1) && i > start {
			if brk > start {
				rows = append(rows, [2]int{start, brk})
				start, cols = brk, cols-brkCols
			} else {
				rows = append(rows, [2]int{start, i})
				start, cols = i, 0
			}
			brk = -1
			continue
		}
		cols += uw
		if space {
			brk, brkCols = j, cols
		}
		i = j
	}
	rows = append(rows, [2]int{start, len(l)})
	if cols > w {
		rows = append(rows, [2]int{len(l), len(l)})
	}
	return rows
}

func (m *editorModel) cursorRow(rows []vrow) int {
	for k, r := range rows {
		last := k+1 == len(rows) || rows[k+1].line != r.line
		if r.line == m.row && m.col >= r.start && (m.col < r.end || last) {
			return k
		}
	}
	return 0
}

// moveTo puts the cursor on row k, at the goal column or the nearest.
func (m *editorModel) moveTo(rows []vrow, k int) {
	cur := rows[m.cursorRow(rows)]
	if m.goal < 0 {
		m.goal = ansi.StringWidth(string(m.lines[m.row][cur.start:m.col]))
	}
	r := rows[k]
	line := m.lines[r.line]
	i, cols := r.start, 0
	for i < r.end && cols+runeWidth(line[i]) <= m.goal {
		cols += runeWidth(line[i])
		i++
	}
	if last := k+1 == len(rows) || rows[k+1].line != r.line; !last && i == r.end && i > r.start {
		i-- // the end of a wrapped row is the start of the next one
	}
	m.row, m.col = r.line, i
}

// externalEditor opens $VISUAL or $EDITOR (vi) on the draft.
func (m *editorModel) externalEditor() tea.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	f, err := os.CreateTemp("", "docsgpt-*.md")
	if err != nil {
		return nil
	}
	f.WriteString(m.expanded())
	f.Close()
	args := append(strings.Fields(editor), f.Name())
	return tea.Exec(handoff(func() error {
		c := exec.Command(args[0], args[1:]...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}), func(err error) tea.Msg { return editedMsg{f.Name(), err} })
}

// isCommand reports whether name is one of the commands.
func (m *editorModel) isCommand(name string) bool {
	return slices.ContainsFunc(m.commands, func(c Command) bool { return c.Name == name })
}

func (m *editorModel) selected() string {
	return m.popup.Items[m.popup.matches[m.popup.cursor]].Label[1:]
}

// refreshPopup shows the commands matching a "/word" input: prefix matches
// first, then fuzzy ones.
func (m *editorModel) refreshPopup() {
	text := m.text()
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \t\n") || text == m.shut || len(m.commands) == 0 {
		m.popup = nil
		return
	}
	q := text[1:]
	type hit struct{ i, score int }
	var hits []hit
	for i, c := range m.commands {
		if strings.HasPrefix(c.Name, q) {
			hits = append(hits, hit{i, -1 << 20})
		} else if s, ok := fuzzyScore(q, c.Name); ok {
			hits = append(hits, hit{i, s})
		}
	}
	if len(hits) == 0 {
		m.popup = nil
		return
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score < hits[b].score })
	items := make([]Item, len(m.commands))
	for i, c := range m.commands {
		items[i] = Item{Label: "/" + c.Name, Description: c.Description}
	}
	m.popup = newSelectModel(Select{Items: items})
	m.popup.matches = nil
	for _, h := range hits {
		m.popup.matches = append(m.popup.matches, h.i)
	}
	m.popup.cursor = 0
}

// view draws the editor in width columns, the footer line last, taking
// at most rows rows (at least 4); without focus it has no cursor.
func (m *editorModel) view(width, rows int, focus bool) []string {
	m.width = max(width, 10)
	layout := m.layout()
	k := m.cursorRow(layout)
	popupRows := 0
	if m.popup != nil {
		m.popup.setSize(m.width, rows)
		m.popup.maxRows = clamp(rows-6, 1, 8)
		popupRows = min(len(m.popup.matches), m.popup.maxRows)
		if len(m.popup.matches) > popupRows {
			popupRows++
		}
	}
	visible := clamp(rows-3-popupRows, 1, max(5, m.height*3/10))
	if k < m.top {
		m.top = k
	} else if k >= m.top+visible {
		m.top = k - visible + 1
	}
	top := clamp(m.top, 0, max(0, len(layout)-visible))
	m.top = top
	end := min(len(layout), top+visible)

	dim := fg(Colors.Dim)
	lines := []string{dim.Render(rule(m.width, "↑", top))}
	for i := top; i < end; i++ {
		lines = append(lines, m.renderRow(layout[i], focus && i == k))
	}
	lines = append(lines, dim.Render(rule(m.width, "↓", len(layout)-end)))
	if m.popup != nil {
		lines = append(lines, m.popup.list()...)
	}
	left, right := m.footer, m.status
	if !m.quitAt.IsZero() {
		left, right = "press ctrl+c again to quit", ""
	}
	if right != "" {
		// Cut from the start: the end has the directory's name.
		room := max(0, m.width-lipgloss.Width(right)-2)
		if w := lipgloss.Width(left); w > room {
			left = ansi.TruncateLeft(left, w-room+1, "…")
		}
		left += strings.Repeat(" ", max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right))) + right
	}
	lines = append(lines, dim.Render(left))
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "…")
	}
	return lines
}

// rule is the editor's border, noting rows scrolled out of view.
func rule(width int, arrow string, hidden int) string {
	if hidden == 0 {
		return strings.Repeat("─", width)
	}
	label := "─── " + arrow + " " + strconv.Itoa(hidden) + " more "
	return label + strings.Repeat("─", max(0, width-lipgloss.Width(label)))
}

// renderRow draws one row, paste markers dim and the cursor reversed.
func (m *editorModel) renderRow(r vrow, cursor bool) string {
	line := m.lines[r.line]
	inMarker := make([]bool, len(line))
	for _, mr := range markerRanges(string(line), m.pastes) {
		for i := mr[0]; i < mr[1]; i++ {
			inMarker[i] = true
		}
	}
	var b strings.Builder
	run, marked := "", false
	flush := func() {
		if marked {
			b.WriteString(fg(Colors.Accent).Render(run))
		} else {
			b.WriteString(run)
		}
		run = ""
	}
	for i := r.start; i < r.end; i++ {
		if cursor && i == m.col {
			flush()
			b.WriteString(reverse(string(line[i])))
			continue
		}
		if inMarker[i] != marked {
			flush()
			marked = inMarker[i]
		}
		run += string(line[i])
	}
	flush()
	if cursor && m.col == r.end {
		b.WriteString(reverse(" "))
	}
	return b.String()
}
