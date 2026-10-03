package display

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// Tool blocks are drawn on stderr, so stdout carries only the answer. A
// block is a bold title line ("$ command", "read path"), optional dim
// lines, and a status line ("✓ exit 0 · 1.2s"). With colours, on a
// terminal, it sits on a background padded by a row above and below and a
// column either side, as in pi: neutral while it runs, tinted green or red
// once it ends. Without, the lines below the title are indented.

// termWidth returns the width of the terminal stdout writes to, defaulting
// to 80.
func termWidth() int {
	w, _ := termSize()
	return w
}

// termSize returns stdout's terminal size, defaulting to 80x24.
func termSize() (width, height int) {
	return fdSize(os.Stdout)
}

func fdSize(f *os.File) (width, height int) {
	w, h, err := term.GetSize(f.Fd())
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// stderrWidth returns the width of the terminal stderr writes to.
func stderrWidth() int {
	w, _ := fdSize(os.Stderr)
	return w
}

// box is the tool block being drawn on a background, nil when blocks have
// none.
var box *toolBox

type toolBox struct {
	profile termenv.Profile // stderr's
	bg      string          // the running background's SGR sequence
	width   int
	rows    []string // drawn below the top padding, unpadded
}

// newToolBox returns a box for a new block, or nil when blocks have no
// background: no colours, or stderr is not a terminal of 256 colours.
func newToolBox() *toolBox {
	p := termenv.NewOutput(os.Stderr).ColorProfile()
	if !T.ToolBg || p > termenv.ANSI256 {
		return nil
	}
	b := &toolBox{profile: p, width: stderrWidth()}
	b.bg = b.seq(colToolBg)
	return b
}

// seq returns the SGR sequence setting c as the background.
func (b *toolBox) seq(c lipgloss.CompleteAdaptiveColor) string {
	cc := c.Light
	if darkBackground {
		cc = c.Dark
	}
	v := cc.TrueColor
	if b.profile == termenv.ANSI256 {
		v = cc.ANSI256
	}
	return termenv.CSI + b.profile.Color(v).Sequence(true) + "m"
}

// row renders s as one full-width row on bg, cut to fit. Every reset in s
// sets bg again.
func (b *toolBox) row(bg, s string) string {
	s = ansi.Truncate(s, b.width-2, "…")
	pad := strings.Repeat(" ", max(0, b.width-1-ansi.StringWidth(s)))
	reset := termenv.CSI + termenv.ResetSeq + "m"
	return bg + " " + strings.ReplaceAll(s, reset, reset+bg) + pad + reset
}

// add draws lines as rows of the block.
func (b *toolBox) add(lines ...string) {
	var out strings.Builder
	for _, l := range lines {
		out.WriteString(b.row(b.bg, l) + "\n")
	}
	b.rows = append(b.rows, lines...)
	io.WriteString(os.Stderr, out.String())
}

// end draws the status line and the bottom padding on bg, and redraws the
// rest of the block on bg as far as it is on screen (unless the width
// changed, which may have rewrapped it).
func (b *toolBox) end(bg, status string) {
	out := "\r\x1b[2K" // the terminal may have echoed ^C on this line

	rows := append([]string{""}, b.rows...) // drawn so far: the top padding first
	if w, h := fdSize(os.Stderr); w == b.width {
		rows = rows[max(0, len(rows)-(h-1)):]
		out += fmt.Sprintf("\x1b[%dA", len(rows))
	} else {
		rows = nil
	}
	for _, r := range append(rows, status, "") {
		out += b.row(bg, r) + "\n"
	}
	io.WriteString(os.Stderr, "\x1b[?2026h"+out+"\x1b[?2026l")
}

// ToolTitle opens a tool block: a blank line, then title in bold. Further
// lines of a multi-line title are indented under the first. Title and note
// are printed through Safe.
func ToolTitle(title, note string) {
	lines := strings.Split(strings.TrimRight(title, "\n"), "\n")
	for i := range lines {
		lines[i] = Safe(lines[i])
		if i > 0 {
			lines[i] = "  " + lines[i]
		}
	}
	if note != "" {
		note = T.Dim.Render(Safe(note))
	}
	if box != nil { // an edited command: end the block of the first one
		io.WriteString(os.Stderr, box.row(box.bg, "")+"\n")
		box = nil
	}
	if box = newToolBox(); box == nil {
		for i := range lines {
			lines[i] = T.ToolTitle.Render(lines[i])
		}
		if note != "" {
			lines[len(lines)-1] += " " + note
		}
		fmt.Fprintf(os.Stderr, "\n%s\n", strings.Join(lines, "\n"))
		return
	}
	// A box cuts its rows, so the title is wrapped to show all of it.
	var rows []string
	for _, l := range lines {
		for _, r := range strings.Split(ansi.Wrap(l, box.width-2, ""), "\n") {
			rows = append(rows, T.ToolTitle.Render(r))
		}
	}
	if last := len(rows) - 1; note != "" && lipgloss.Width(rows[last]+" "+note) <= box.width-2 {
		rows[last] += " " + note
	} else if note != "" {
		rows = append(rows, note)
	}
	io.WriteString(os.Stderr, "\n"+box.row(box.bg, "")+"\n")
	box.add(rows...)
}

// ToolLines prints lines of a tool block (a preview). They are printed as
// they are, so must already be safe (DiffPreview's are).
func ToolLines(lines []string) {
	if box != nil {
		box.add(lines...)
		return
	}
	for _, l := range lines {
		fmt.Fprintln(os.Stderr, "  "+l)
	}
}

// ToolStatus closes a tool block: "✓ msg" or "✗ msg", the message dim and
// printed through Safe.
func ToolStatus(ok bool, msg string) {
	glyph, bg := T.Success.Render("✓"), colToolOkBg
	if !ok {
		glyph, bg = T.Error.Render("✗"), colToolFailBg
	}
	status := glyph + " " + T.Dim.Render(Safe(msg))
	if b := box; b != nil {
		box = nil
		b.end(b.seq(bg), status)
		return
	}
	clear := "" // the terminal may have echoed ^C on this line
	if term.IsTerminal(os.Stderr.Fd()) {
		clear = "\r\x1b[2K"
	}
	fmt.Fprintf(os.Stderr, "%s  %s\n", clear, status)
}

// Duration formats d like "0.3s", "1m 5s".
func Duration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	d = d.Round(time.Second)
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// tailRows is how many output lines a running command shows.
const tailRows = 5

// TailView shows a command's output while it runs: on a terminal, only
// the last few lines, redrawn in place. Close leaves those lines in the
// scrollback below a "… N earlier lines" note.
type TailView struct {
	out   io.Writer
	tty   bool
	width int
	box   *toolBox // the block's, which the live region keeps padded

	mu     sync.Mutex
	tail   []string // the last complete lines
	part   string   // the line being written
	total  int      // complete lines seen
	rows   int      // rows the live region occupies
	timer  *time.Timer
	closed bool
}

// NewTailView returns a TailView drawing on stderr.
func NewTailView() *TailView {
	v := &TailView{out: os.Stderr, tty: term.IsTerminal(os.Stderr.Fd()), width: stderrWidth(), box: box}
	if v.box != nil {
		v.draw() // the bottom padding, before any output
	}
	return v
}

func (v *TailView) Write(p []byte) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	lines := strings.Split(v.part+string(p), "\n")
	for _, l := range lines[:len(lines)-1] {
		v.total++
		v.tail = append(v.tail, l)
		if len(v.tail) > tailRows {
			v.tail = v.tail[1:]
		}
	}
	v.part = lines[len(lines)-1]
	if len(v.part) > 4096 { // a line without end (binary output, a progress bar)
		v.part = v.part[len(v.part)-4096:]
	}
	if v.tty && v.timer == nil && !v.closed {
		v.timer = time.AfterFunc(50*time.Millisecond, v.redraw)
	}
	return len(p), nil
}

func (v *TailView) redraw() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.timer = nil
	if !v.closed {
		v.draw()
	}
}

// Close stops the live region and prints the final tail.
func (v *TailView) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.timer != nil {
		v.timer.Stop()
		v.timer = nil
	}
	v.closed = true
	if v.part != "" {
		v.total++
		v.tail = append(v.tail, v.part)
		if len(v.tail) > tailRows {
			v.tail = v.tail[1:]
		}
		v.part = ""
	}
	out := v.draw()
	v.rows = 0
	if v.box != nil {
		v.box.rows = append(v.box.rows, out...)
	}
}

// draw replaces the live region with the current tail and returns its
// lines. In a box the region ends with the bottom padding until Close.
func (v *TailView) draw() []string {
	var b strings.Builder
	if v.rows > 0 {
		b.WriteString("\r\x1b[2K" + strings.Repeat("\x1b[1A\x1b[2K", v.rows))
	}
	lines := v.tail
	if v.part != "" {
		lines = append(slices.Clone(lines), v.part)[max(0, len(lines)+1-tailRows):]
	}
	shown := v.total
	if v.part != "" {
		shown++
	}
	var out []string
	if hidden := shown - len(lines); hidden > 0 {
		out = append(out, T.Dim.Render(fmt.Sprintf("… %d earlier %s", hidden, plural(hidden, "line"))))
	}
	room := v.width - 3
	if v.box != nil {
		room = v.box.width - 2
	}
	for _, l := range lines {
		out = append(out, T.ToolOutput.Render(ansi.Truncate(cleanLine(l), room, "…")))
	}
	rows := len(out)
	for _, l := range out {
		if v.box != nil {
			b.WriteString(v.box.row(v.box.bg, l) + "\n")
		} else {
			b.WriteString("  " + l + "\n")
		}
	}
	if v.box != nil && !v.closed {
		b.WriteString(v.box.row(v.box.bg, "") + "\n")
		rows++
	}
	if v.tty {
		v.rows = rows
		io.WriteString(v.out, "\x1b[?2026h"+b.String()+"\x1b[?2026l")
	} else {
		io.WriteString(v.out, b.String())
	}
	return out
}

// cleanLine makes a line of command output safe and readable on one row:
// escape sequences (colours) dropped, only what follows a carriage return
// kept (progress bars redraw that way), the rest through Safe.
func cleanLine(s string) string {
	s = strings.TrimRight(ansi.Strip(s), "\r")
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	return Safe(s)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// DiffPreview renders the first limit changed lines between before and
// after ("12 + text", "12 - text") and counts every added and removed line.
func DiffPreview(before, after string, limit int) (preview []string, added, removed int) {
	type op struct {
		add  bool
		line int
		text string
	}
	a, b := splitLines(before), splitLines(after)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]

	var ops []op
	if len(ma)*len(mb) > 1<<20 { // too big to align: all out, all in
		for i, l := range ma {
			ops = append(ops, op{false, pre + i + 1, l})
		}
		for j, l := range mb {
			ops = append(ops, op{true, pre + j + 1, l})
		}
	} else {
		// lcs[i][j]: longest common subsequence of ma[i:] and mb[j:].
		w := len(mb) + 1
		lcs := make([]int32, (len(ma)+1)*w)
		for i := len(ma) - 1; i >= 0; i-- {
			for j := len(mb) - 1; j >= 0; j-- {
				if ma[i] == mb[j] {
					lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
				} else {
					lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
				}
			}
		}
		i, j := 0, 0
		for i < len(ma) || j < len(mb) {
			switch {
			case i < len(ma) && j < len(mb) && ma[i] == mb[j]:
				i, j = i+1, j+1
			case i < len(ma) && (j == len(mb) || lcs[(i+1)*w+j] >= lcs[i*w+j+1]):
				ops = append(ops, op{false, pre + i + 1, ma[i]})
				i++
			default:
				ops = append(ops, op{true, pre + j + 1, mb[j]})
				j++
			}
		}
	}

	numW := len(strconv.Itoa(len(a) + len(b)))
	width := stderrWidth() - numW - 6
	for k, o := range ops {
		if o.add {
			added++
		} else {
			removed++
		}
		if k >= limit {
			continue
		}
		sign, style := "- ", T.Error
		if o.add {
			sign, style = "+ ", T.Success
		}
		num := fmt.Sprintf("%*d", numW, o.line)
		preview = append(preview, T.Dim.Render(num)+" "+style.Render(sign+ansi.Truncate(Safe(o.text), width, "…")))
	}
	if more := len(ops) - limit; more > 0 {
		preview = append(preview, T.Dim.Render(fmt.Sprintf("… %d more changed %s", more, plural(more, "line"))))
	}
	return preview, added, removed
}

// splitLines splits s into lines; a final newline ends the last line
// rather than starting an empty one.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
