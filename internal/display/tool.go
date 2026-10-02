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

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// Tool blocks are drawn on stderr, so stdout carries only the answer. A
// block is a bold title line ("$ command", "read path"), optional dim
// lines, and a status line ("✓ exit 0 · 1.2s").

// termWidth returns the width of the terminal stdout writes to, defaulting
// to 80.
func termWidth() int {
	w, _ := termSize()
	return w
}

// termSize returns stdout's terminal size, defaulting to 80x24.
func termSize() (width, height int) {
	w, h, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// stderrWidth returns the width of the terminal stderr writes to.
func stderrWidth() int {
	w, _, err := term.GetSize(os.Stderr.Fd())
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// ToolTitle opens a tool block: a blank line, then title in bold. Further
// lines of a multi-line title are indented under the first.
func ToolTitle(title, note string) {
	lines := strings.Split(strings.TrimRight(title, "\n"), "\n")
	for i := range lines {
		if i > 0 {
			lines[i] = "  " + lines[i]
		}
		lines[i] = T.ToolTitle.Render(lines[i])
	}
	if note != "" {
		lines[len(lines)-1] += " " + T.Dim.Render(note)
	}
	fmt.Fprintf(os.Stderr, "\n%s\n", strings.Join(lines, "\n"))
}

// ToolLines prints lines of a tool block (a preview), indented.
func ToolLines(lines []string) {
	for _, l := range lines {
		fmt.Fprintln(os.Stderr, "  "+l)
	}
}

// ToolStatus closes a tool block: "✓ msg" or "✗ msg", the message dim.
func ToolStatus(ok bool, msg string) {
	glyph := T.Success.Render("✓")
	if !ok {
		glyph = T.Error.Render("✗")
	}
	fmt.Fprintf(os.Stderr, "  %s %s\n", glyph, T.Dim.Render(msg))
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
	return &TailView{out: os.Stderr, tty: term.IsTerminal(os.Stderr.Fd()), width: stderrWidth()}
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
	v.draw()
	v.rows = 0
}

// draw replaces the live region with the current tail.
func (v *TailView) draw() {
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
	for _, l := range lines {
		out = append(out, T.ToolOutput.Render(ansi.Truncate(cleanLine(l), v.width-3, "…")))
	}
	for _, l := range out {
		b.WriteString("  " + l + "\n")
	}
	if v.tty {
		v.rows = len(out)
		io.WriteString(v.out, "\x1b[?2026h"+b.String()+"\x1b[?2026l")
	} else {
		io.WriteString(v.out, b.String())
	}
}

// cleanLine makes a line of command output safe to draw on one row: no
// escape sequences or control characters, only what follows a carriage
// return (progress bars redraw that way), tabs expanded.
func cleanLine(s string) string {
	s = ansi.Strip(s)
	if i := strings.LastIndexByte(strings.TrimRight(s, "\r"), '\r'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return -1
		}
		return r
	}, s)
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
		preview = append(preview, T.Dim.Render(num)+" "+style.Render(sign+ansi.Truncate(cleanLine(o.text), width, "…")))
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
