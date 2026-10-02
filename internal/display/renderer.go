package display

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

// frameInterval throttles redraws of the block being streamed (~30fps).
const frameInterval = 33 * time.Millisecond

// StreamRenderer prints a streamed markdown answer to stdout.
//
// On a terminal, every finished block is rendered once and committed to
// scrollback. The block still being written is drawn below it and redrawn in
// place, but only while it fits on screen: lines that scrolled away cannot be
// erased, so a taller block waits until it is complete. Anywhere else the raw
// text is written as it arrives, and nothing more.
type StreamRenderer struct {
	ShowReasoning bool

	out    io.Writer
	tty    bool
	md     *glamour.TermRenderer
	width  int
	height int

	mu       sync.Mutex
	content  strings.Builder // the whole answer
	pending  string          // streamed text not committed yet
	started  bool            // something is on screen: blocks get a blank line before them
	liveRows int             // terminal rows the live block occupies
	tooTall  bool            // the live block outgrew the screen: wait for it to finish
	midLine  bool            // the cursor is mid-line (reasoning, or raw text off a terminal)
	lastDraw time.Time
	timer    *time.Timer
}

// NewStreamRenderer creates a StreamRenderer writing to stdout.
func NewStreamRenderer() *StreamRenderer {
	r := &StreamRenderer{out: os.Stdout, tty: isatty.IsTerminal(os.Stdout.Fd())}
	if r.tty {
		r.width, r.height = termSize()
		r.md = newMarkdown(r.width)
	}
	return r
}

// Delta processes one streamed delta. Reasoning is shown only on a terminal
// with ShowReasoning set, and never inside a rendered block.
func (r *StreamRenderer) Delta(delta docsgpt.Delta) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if delta.ReasoningContent != "" && r.ShowReasoning && r.tty {
		var b strings.Builder
		r.erase(&b)
		b.WriteString(T.Reasoning.Render(delta.ReasoningContent))
		io.WriteString(r.out, b.String())
		r.midLine = !strings.HasSuffix(delta.ReasoningContent, "\n")
		r.started = true
	}
	if delta.Content == "" {
		return
	}
	r.content.WriteString(delta.Content)
	if !r.tty {
		io.WriteString(r.out, delta.Content)
		r.midLine = !strings.HasSuffix(delta.Content, "\n")
		return
	}
	r.pending += delta.Content
	if wait := frameInterval - time.Since(r.lastDraw); wait > 0 {
		if r.timer == nil {
			var t *time.Timer
			t = time.AfterFunc(wait, func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				if r.timer == t { // not stopped by Flush meanwhile
					r.timer = nil
					r.draw(false)
				}
			})
			r.timer = t
		}
		return
	}
	r.draw(false)
}

// Flush commits everything streamed so far, finished or not. Call it before
// anything else is printed (a tool call, an error) and at the end.
func (r *StreamRenderer) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if r.tty {
		r.draw(true)
	}
	if r.midLine {
		io.WriteString(r.out, "\n")
		r.midLine = false
	}
}

// Content returns the raw accumulated content.
func (r *StreamRenderer) Content() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.content.String()
}

// draw commits the finished blocks of pending (all of it when final) and
// redraws the live block below them, in one synchronized write.
func (r *StreamRenderer) draw(final bool) {
	r.lastDraw = time.Now()
	var b strings.Builder
	r.erase(&b)
	if r.midLine {
		b.WriteString("\n")
		r.midLine = false
	}

	cut := len(r.pending)
	if !final {
		cut = commitPoint(r.pending)
	}
	if cut > 0 {
		if block := r.block(r.pending[:cut]); block != "" {
			b.WriteString(block)
			r.started = true
		}
		r.pending = r.pending[cut:]
		r.tooTall = false
	}

	if !r.tooTall {
		block := r.block(r.pending)
		rows := r.rows(block)
		r.tooTall = rows >= r.height // the cursor needs a row below it
		if rows > 0 && !r.tooTall {
			b.WriteString(block)
			r.liveRows = rows
		}
	}
	if b.Len() > 0 {
		io.WriteString(r.out, "\x1b[?2026h"+b.String()+"\x1b[?2026l")
	}
}

// block renders md as it is printed: separated from what came before by a
// blank line, ending with a newline. Empty when md renders to nothing.
func (r *StreamRenderer) block(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}
	s := renderMarkdown(r.md, md)
	if s == "" {
		return ""
	}
	if r.started {
		s = "\n" + s
	}
	return s + "\n"
}

// rows counts the terminal rows block takes, wrapped lines included.
func (r *StreamRenderer) rows(block string) int {
	if block == "" {
		return 0
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSuffix(block, "\n"), "\n") {
		n += max(1, (lipgloss.Width(line)+r.width-1)/r.width)
	}
	return n
}

// erase removes the live block; the cursor sits on the line below it. It
// clears line by line: clearing to the end of the screen from its top-left
// corner makes tmux push the whole screen into the scrollback.
func (r *StreamRenderer) erase(b *strings.Builder) {
	if r.liveRows > 0 {
		b.WriteString("\r" + strings.Repeat("\x1b[1A\x1b[2K", r.liveRows))
		r.liveRows = 0
	}
}

// commitPoint returns the length of the leading run of finished blocks in
// text. A block is finished by a blank line, or by the closing line of an
// unindented code fence, once the next block has begun on an unindented line;
// an indented one may still belong to the block (a list item's continuation).
func commitPoint(text string) int {
	cut, ended := 0, false
	var fence string // marker of the open code fence, "" outside one
	topFence := false
	for off := 0; off < len(text); {
		line, complete := text[off:], false
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line, complete = line[:i], true
		}
		trimmed := strings.TrimLeft(line, " \t")
		indented := len(trimmed) < len(line)

		switch {
		case fence != "":
			if !complete {
				return cut
			}
			if closesFence(trimmed, fence) {
				fence, ended = "", topFence
			}
		case trimmed == "":
			if complete {
				ended = true
			}
		default:
			if ended && !indented {
				cut = off
			}
			ended = false
			if complete {
				fence, topFence = opensFence(trimmed), !indented
			}
		}
		if !complete {
			break
		}
		off += len(line) + 1
	}
	return cut
}

// opensFence returns the marker of the code fence line opens, or "".
func opensFence(line string) string {
	for _, c := range []string{"`", "~"} {
		n := len(line) - len(strings.TrimLeft(line, c))
		if n >= 3 && !(c == "`" && strings.Contains(line[n:], "`")) {
			return line[:n]
		}
	}
	return ""
}

// closesFence reports whether line closes the fence opened with marker.
func closesFence(line, marker string) bool {
	line = strings.TrimRight(line, " \t")
	return len(line) >= len(marker) && strings.Trim(line, marker[:1]) == ""
}
