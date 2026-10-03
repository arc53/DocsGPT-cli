package display

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

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
//
// Either way the text loses its terminal control sequences first (see
// StripControls): the model must not be able to hide text, retitle or
// recolour the terminal, write the clipboard (OSC 52) or move the cursor
// under the redraws. Off a terminal too, since piped output often ends up
// on one (a pager, a file that is cat-ed); newlines and tabs stay.
type StreamRenderer struct {
	ShowReasoning bool

	spin ui.Spinner

	out    io.Writer
	tty    bool
	md     *markdown
	width  int
	height int

	mu        sync.Mutex
	answer    controlFilter
	reasoning controlFilter
	content   strings.Builder // the whole answer
	pending   string          // streamed text not committed yet
	list      olist           // how the committed text ends
	started   bool            // something is on screen: blocks get a blank line before them
	liveRows  int             // terminal rows the live block occupies
	tooTall   bool            // the live block outgrew the screen: wait for it to finish
	midLine   bool            // the cursor is mid-line (reasoning, or raw text off a terminal)
	thinking  bool            // a reasoning block is being written
	lastDraw  time.Time
	timer     *time.Timer
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

// Wait shows a "Thinking…" spinner on stderr until the next visible output
// or Flush.
func (r *StreamRenderer) Wait() { r.spin.Start("Thinking…") }

// Delta processes one streamed delta. Reasoning is shown only on a terminal
// with ShowReasoning set, as a dim block of its own: the answer streamed so
// far is committed before it, and the answer resumes after a blank line.
func (r *StreamRenderer) Delta(delta docsgpt.Delta) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if text := r.reasoning.clean(delta.ReasoningContent); text != "" && r.ShowReasoning && r.tty {
		r.spin.Stop()
		if !r.thinking {
			if r.pending != "" {
				r.draw(true)
			}
			if r.started {
				io.WriteString(r.out, "\n")
			}
			r.thinking, r.started = true, true
		}
		io.WriteString(r.out, paint(T.Thinking, text))
		r.midLine = !strings.HasSuffix(text, "\n")
	}
	text := r.answer.clean(delta.Content) // CRs go too: CRLF becomes LF
	if text == "" {
		return
	}
	r.spin.Stop()
	r.thinking = false
	r.content.WriteString(text)
	if !r.tty {
		io.WriteString(r.out, text)
		r.midLine = !strings.HasSuffix(text, "\n")
		return
	}
	r.pending += text
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
	r.spin.Stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.thinking = false
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
	r.started = true // what follows (a tool block) came after something
}

// Started reports whether anything was streamed, shown or not yet.
func (r *StreamRenderer) Started() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started || r.content.Len() > 0
}

// Content returns the answer so far, without control sequences.
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
		if block := r.block(r.pending[:cut], &r.list); block != "" {
			b.WriteString(block)
			r.started = true
		}
		r.pending = r.pending[cut:]
		r.tooTall = false
	}

	if !r.tooTall {
		list := r.list // drawn again with the next delta
		block := r.block(r.pending, &list)
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
// blank line, ending with a newline. Empty when md renders to nothing. list
// is how the blocks before it end (see renderMarkdown).
func (r *StreamRenderer) block(md string, list *olist) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}
	s := renderMarkdown(r.md, r.width, md, list)
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
	line = strings.TrimRight(line, " \t\r")
	return len(line) >= len(marker) && strings.Trim(line, marker[:1]) == ""
}
