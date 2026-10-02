package ui

import (
	"io"
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

// Editor is the chat input: multi-line text between two dim rules, a dim
// footer line under them, prompt history on ↑/↓, large pastes collapsed to
// a marker, and a popup of the slash commands.
type Editor struct {
	Commands []Command
	History  *History // nil for none
	Footer   string   // left of the footer line
	Status   string   // right of the footer line
}

// PasteMarker matches the marker a collapsed paste leaves in the editor.
var PasteMarker = regexp.MustCompile(`\[paste #(\d+) (?:\+\d+ lines|\d+ chars)\]`)

// quitWindow is how soon a second Ctrl+C on an empty editor quits.
const quitWindow = time.Second

// Run edits one message. It returns the text with pastes expanded, and as
// shown (pastes as their markers), or io.EOF when the user quits: Ctrl+D on
// an empty editor, or Ctrl+C twice.
func (e *Editor) Run() (text, shown string, err error) {
	m := newEditorModel(e)
	m.tty = newTTYInput()
	if _, err := run(m, false, m.tty); err != nil {
		return "", "", err
	}
	if m.quit {
		return "", "", io.EOF
	}
	// Text typed ahead during an answer arrives with its line end.
	return strings.TrimRight(m.expanded(), "\n"), strings.TrimRight(m.text(), "\n"), nil
}

type editorModel struct {
	*Editor
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
	done     bool
	quit     bool
	away     bool // in $EDITOR: the frame is cleared, or it would stay above

	tty *ttyInput // nil off a Unix terminal
}

type (
	hintMsg   struct{}
	editedMsg struct {
		path string
		err  error
	}
)

func newEditorModel(e *Editor) *editorModel {
	if e.History == nil {
		e.History = LoadHistory("")
	}
	return &editorModel{
		Editor: e, lines: [][]rune{{}}, goal: -1, pastes: map[int]string{},
		hist: len(e.History.entries), width: 80, height: 24,
	}
}

func (m *editorModel) setSize(w, h int) { m.width, m.height = max(w, 10), max(h, 5) }

func (m *editorModel) Init() tea.Cmd { return nil }

func (m *editorModel) text() string {
	parts := make([]string, len(m.lines))
	for i, l := range m.lines {
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

func (m *editorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.setSize(msg.Width, msg.Height)
	case hintMsg:
		if time.Since(m.quitAt) >= quitWindow {
			m.quitAt = time.Time{}
		}
	case editedMsg:
		m.away = false
		if data, err := os.ReadFile(msg.path); err == nil && msg.err == nil {
			m.setText(strings.TrimRight(string(data), "\n"))
			m.pastes = map[int]string{}
			m.changed()
		}
		os.Remove(msg.path)
	case tea.KeyMsg:
		before, row, col := m.text(), m.row, m.col
		cmd := m.key(msg)
		m.snap(row, col)
		if s := msg.String(); m.text() != before && s != "up" && s != "down" {
			m.changed()
		}
		return m, cmd
	}
	return m, nil
}

// changed follows an edit: history browsing ends, pastes whose marker is
// gone are dropped and the popup refreshes.
func (m *editorModel) changed() {
	m.hist = len(m.History.entries)
	kept := map[int]string{}
	for _, sub := range PasteMarker.FindAllStringSubmatch(m.text(), -1) {
		if id, _ := strconv.Atoi(sub[1]); m.pastes[id] != "" {
			kept[id] = m.pastes[id]
		}
	}
	m.pastes = kept
	m.refreshPopup()
}

func (m *editorModel) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	if s != "ctrl+c" {
		m.quitAt = time.Time{}
	}
	if s != "up" && s != "down" {
		m.goal = -1
	}
	if k.Paste {
		m.paste(string(k.Runes))
		return nil
	}
	if m.popup != nil {
		switch s {
		case "up", "ctrl+p", "down", "ctrl+n":
			m.popup.move(strings.TrimPrefix(s, "ctrl+"))
			return nil
		case "tab":
			m.setText("/" + m.selected() + " ")
			m.popup = nil
			return nil
		case "enter":
			if !m.isCommand(strings.TrimPrefix(m.text(), "/")) {
				m.setText("/" + m.selected())
			}
			return m.submit()
		case "esc":
			m.popup, m.shut = nil, m.text()
			return nil
		}
	}

	line := m.lines[m.row]
	switch s {
	case "ctrl+c":
		if !m.empty() {
			m.lines, m.row, m.col, m.pastes = [][]rune{{}}, 0, 0, map[int]string{}
			m.quitAt = time.Time{}
			return nil
		}
		if !m.quitAt.IsZero() && time.Since(m.quitAt) < quitWindow {
			return m.finish(true)
		}
		m.quitAt = time.Now()
		return tea.Tick(quitWindow, func(time.Time) tea.Msg { return hintMsg{} })
	case "ctrl+d":
		if m.empty() {
			return m.finish(true)
		}
		m.deleteForward()
	case "enter":
		if m.col > 0 && line[m.col-1] == '\\' {
			m.lines[m.row] = slices.Delete(line, m.col-1, m.col)
			m.col--
			m.insert("\n")
			return nil
		}
		if strings.TrimSpace(m.text()) == "" {
			return nil
		}
		return m.submit()
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
		return m.externalEditor()
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
	return nil
}

func (m *editorModel) finish(quit bool) tea.Cmd {
	m.done, m.quit, m.popup = true, quit, nil
	return tea.Quit
}

func (m *editorModel) submit() tea.Cmd {
	m.History.Add(m.expanded())
	return m.finish(false)
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
	entries := m.History.entries
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
	entries := m.History.entries
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
// cursor at the end of a full row.
func (m *editorModel) layout() []vrow {
	w := max(1, m.width-1)
	var rows []vrow
	for i, l := range m.lines {
		start, cols := 0, 0
		for j, r := range l {
			if rw := runeWidth(r); cols+rw > w && j > start {
				rows = append(rows, vrow{i, start, j})
				start, cols = j, rw
			} else {
				cols += rw
			}
		}
		rows = append(rows, vrow{i, start, len(l)})
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
	m.away = true
	done := func(err error) tea.Msg { return editedMsg{f.Name(), err} }
	if m.tty == nil {
		return tea.ExecProcess(exec.Command(args[0], args[1:]...), done)
	}
	return tea.Exec(plainKeys{exec.Command(args[0], args[1:]...)}, done)
}

// plainKeys runs a program on the terminal itself (bubbletea would hand it
// ttyInput, which an exec.Cmd copies through a pipe), with the key reports
// as they were.
type plainKeys struct{ *exec.Cmd }

func (c plainKeys) SetStdin(r io.Reader)  { c.Stdin = r.(*ttyInput).File }
func (c plainKeys) SetStdout(w io.Writer) { c.Stdout = w }
func (c plainKeys) SetStderr(w io.Writer) { c.Stderr = w }

func (c plainKeys) Run() error {
	os.Stdout.WriteString(keysOff)
	defer os.Stdout.WriteString(keysOn)
	return c.Cmd.Run()
}

// isCommand reports whether name is one of the commands.
func (m *editorModel) isCommand(name string) bool {
	return slices.ContainsFunc(m.Commands, func(c Command) bool { return c.Name == name })
}

func (m *editorModel) selected() string {
	return m.popup.Items[m.popup.matches[m.popup.cursor]].Label[1:]
}

// refreshPopup shows the commands matching a "/word" input: prefix matches
// first, then fuzzy ones.
func (m *editorModel) refreshPopup() {
	text := m.text()
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \t\n") || text == m.shut || len(m.Commands) == 0 {
		m.popup = nil
		return
	}
	q := text[1:]
	type hit struct{ i, score int }
	var hits []hit
	for i, c := range m.Commands {
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
	items := make([]Item, len(m.Commands))
	for i, c := range m.Commands {
		items[i] = Item{Label: "/" + c.Name, Description: c.Description}
	}
	m.popup = newSelectModel(Select{Items: items})
	m.popup.matches = nil
	for _, h := range hits {
		m.popup.matches = append(m.popup.matches, h.i)
	}
	m.popup.cursor = 0
}

func (m *editorModel) View() string {
	if m.done || m.away {
		return summary("")
	}
	rows := m.layout()
	k := m.cursorRow(rows)
	popupRows := 0
	if m.popup != nil {
		m.popup.setSize(m.width, m.height)
		m.popup.maxRows = clamp(m.height-6, 1, 8)
		popupRows = min(len(m.popup.matches), m.popup.maxRows)
		if len(m.popup.matches) > popupRows {
			popupRows++
		}
	}
	visible := clamp(m.height-3-popupRows, 1, max(5, m.height*3/10))
	if k < m.top {
		m.top = k
	} else if k >= m.top+visible {
		m.top = k - visible + 1
	}
	top := clamp(m.top, 0, max(0, len(rows)-visible))
	m.top = top
	end := min(len(rows), top+visible)

	dim := fg(Colors.Dim)
	lines := []string{dim.Render(rule(m.width, "↑", top))}
	for i := top; i < end; i++ {
		lines = append(lines, m.renderRow(rows[i], i == k))
	}
	lines = append(lines, dim.Render(rule(m.width, "↓", len(rows)-end)))
	if m.popup != nil {
		lines = append(lines, m.popup.list()...)
	}
	left, right := m.Footer, m.Status
	if !m.quitAt.IsZero() {
		left, right = "press ctrl+c again to quit", ""
	}
	if right != "" {
		room := m.width - lipgloss.Width(right) - 2
		left = ansi.Truncate(left, max(room, 0), "…")
		left += strings.Repeat(" ", max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right))) + right
	}
	lines = append(lines, dim.Render(ansi.Truncate(left, m.width, "…")))
	return frame(lines, m.width)
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
