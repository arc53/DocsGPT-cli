package display

import (
	"fmt"
	"strings"
	"sync"

	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// The blocks of the chat's transcript (ui.Block): each renders itself at
// the screen's width, once per width unless it changes.

// cached is a block that does not change, rendered once per width.
type cached struct {
	mu     sync.Mutex
	width  int
	lines  []string
	render func(width int) []string
}

func newCached(render func(width int) []string) *cached { return &cached{width: -1, render: render} }

func (c *cached) Lines(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.width != width {
		c.lines, c.width = fit(c.render(width), width), width
	}
	return c.lines
}

// fit splits multi-line entries and cuts lines wider than width (a long
// line of code) into rows, which the screen would otherwise cut off; the
// rows after the first are indented as the line is.
func fit(lines []string, width int) []string {
	var out []string
	for _, l := range lines {
		for _, s := range strings.Split(l, "\n") {
			w := ansi.StringWidth(s)
			if w <= width {
				out = append(out, s)
				continue
			}
			plain := ansi.Strip(s)
			indent := min(len(plain)-len(strings.TrimLeft(plain, " ")), width/2)
			out = append(out, ansi.Cut(s, 0, width))
			for at := width; at < w; at += width - indent {
				out = append(out, strings.Repeat(" ", indent)+ansi.Cut(s, at, at+width-indent))
			}
		}
	}
	return out
}

// Text is a block of lines that are styled already.
func Text(s string) ui.Block {
	return newCached(func(int) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") })
}

// Note is a dim notice, wrapped to the width.
func Note(s string) ui.Block { return styled(T.Dim, "", s) }

// Failure is "✗ message" in the error colour, without control sequences.
func Failure(msg string) ui.Block { return styled(T.Error, "✗ ", StripControls(msg)) }

// Done is a "✓" and a dim notice.
func Done(msg string) ui.Block {
	return newCached(func(width int) []string {
		lines := wrap(msg, width-2)
		for i, l := range lines {
			if i == 0 {
				lines[i] = T.Success.Render("✓") + " " + T.Dim.Render(l)
			} else {
				lines[i] = "  " + T.Dim.Render(l)
			}
		}
		return lines
	})
}

// styled wraps prefix and s at the width and styles each line.
func styled(style lipgloss.Style, prefix, s string) ui.Block {
	return newCached(func(width int) []string {
		lines := wrap(prefix+s, width)
		for i, l := range lines {
			lines[i] = style.Render(l)
		}
		return lines
	})
}

func wrap(s string, width int) []string {
	return strings.Split(ansi.Wrap(strings.TrimRight(s, "\n"), max(width, 10), ""), "\n")
}

// Markdown is a block of rendered markdown (an answer shown again).
func Markdown(md string) ui.Block {
	md = StripControls(md)
	return newCached(func(width int) []string {
		if out := renderMarkdown(markdownAt(width), width, md); out != "" {
			return strings.Split(out, "\n")
		}
		return nil
	})
}

// Sources is the "Sources" block of an answer, empty without any.
func Sources(sources []docsgpt.Source) ui.Block {
	return newCached(func(width int) []string { return sourceLines(sources, width) })
}

// Header is what opens a chat: the banner (per setting, once by default),
// the name, the key hints and the instruction files the context sends.
func Header(version, newline string, files []string, banner string) ui.Block {
	show := showBanner(banner)
	return newCached(func(width int) []string {
		var lines []string
		if show {
			lines = append(bannerLines(width), "")
		}
		return append(lines, strings.Split(chatHeader(width, version, newline, files), "\n")...)
	})
}

// userBlock is a message the user sent.
type userBlock struct{ *cached }

func (userBlock) Prompt() {}

// User is the block of a message the user sent: on a subtle background,
// or after a "❯" without colours.
func User(text string) ui.Block {
	return userBlock{newCached(func(width int) []string { return strings.Split(userMessage(width, text), "\n") })}
}

// markdownAt returns a markdown renderer for width, kept per width: a new
// one costs a few milliseconds. Only the screen's goroutine renders.
func markdownAt(width int) *glamour.TermRenderer {
	mdMu.Lock()
	defer mdMu.Unlock()
	if mdWidth != width || mdRenderer == nil {
		mdRenderer, mdWidth = newMarkdown(width), width
	}
	return mdRenderer
}

var (
	mdMu       sync.Mutex
	mdRenderer *glamour.TermRenderer
	mdWidth    int
)

// Answer is a streamed answer: the model's reasoning (dim, when shown) and
// the markdown text. The finished markdown blocks are rendered once; only
// the one being written is rendered again as it grows.
type Answer struct {
	showReasoning bool

	mu        sync.Mutex
	answer    controlFilter
	reasoning controlFilter
	content   strings.Builder
	thought   strings.Builder
	done      bool
	version   int // changes with every delta

	width     int
	cut       int      // bytes of content rendered into committed
	committed []string // the finished blocks
	lines     []string
	drawn     int // the version lines are of
}

// NewAnswer returns an empty answer; showReasoning shows the reasoning.
func NewAnswer(showReasoning bool) *Answer {
	return &Answer{showReasoning: showReasoning, width: -1}
}

// Delta adds a streamed delta, cleaned of control sequences, and reports
// whether it shows anything.
func (a *Answer) Delta(d docsgpt.Delta) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	thought := a.reasoning.clean(d.ReasoningContent)
	text := a.answer.clean(d.Content)
	a.thought.WriteString(thought)
	a.content.WriteString(text)
	a.version++
	return text != "" || thought != "" && a.showReasoning
}

// Finish marks the answer complete: its last block is final.
func (a *Answer) Finish() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.done = true
	a.version++
}

// Content returns the answer's text, without control sequences.
func (a *Answer) Content() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.content.String()
}

func (a *Answer) Lines(width int) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if width != a.width {
		a.width, a.cut, a.committed, a.drawn = width, 0, nil, -1
	}
	if a.drawn == a.version {
		return a.lines
	}
	md := markdownAt(width)
	content := a.content.String()
	end := len(content)
	if !a.done {
		end = a.cut + commitPoint(content[a.cut:])
	}
	if end > a.cut {
		if out := renderMarkdown(md, width, content[a.cut:end]); out != "" {
			if len(a.committed) > 0 {
				a.committed = append(a.committed, "")
			}
			a.committed = append(a.committed, fit(strings.Split(out, "\n"), width)...)
		}
		a.cut = end
	}

	var lines []string
	if t := strings.TrimSpace(a.thought.String()); t != "" && a.showReasoning {
		for _, l := range wrap(t, width) {
			lines = append(lines, paint(T.Thinking, l))
		}
	}
	add := func(part []string) {
		if len(part) == 0 {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, part...)
	}
	add(a.committed)
	if tail := renderMarkdown(md, width, content[a.cut:]); tail != "" {
		add(fit(strings.Split(tail, "\n"), width))
	}
	a.lines, a.drawn = lines, a.version
	return lines
}

// ToolBlock shows a tool call in the transcript: its title, a preview
// (a diff), the tail of a command's output and a status line, on a
// background tinted by how it ended (as the stderr blocks of ask are).
type ToolBlock struct {
	mu      sync.Mutex
	title   string
	note    string
	preview []string
	tail    []string // the last complete output lines
	part    string   // the output line being written
	total   int      // complete output lines seen
	closed  bool
	ok      bool
	status  string
	version int
	c       struct {
		width, version int
		lines          []string
	}
}

// NewToolBlock opens a block for a tool call; title and note are shown
// through Safe.
func NewToolBlock(title, note string) *ToolBlock {
	b := &ToolBlock{title: title, note: note}
	b.c.width = -1
	return b
}

// AddLines adds preview lines, which must be safe already (DiffPreview's
// are).
func (b *ToolBlock) AddLines(lines []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.preview = append(b.preview, lines...)
	b.version++
}

// Write adds a command's output; the block shows its last lines.
func (b *ToolBlock) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := strings.Split(b.part+string(p), "\n")
	for _, l := range lines[:len(lines)-1] {
		b.total++
		b.tail = append(b.tail, l)
		if len(b.tail) > tailRows {
			b.tail = b.tail[1:]
		}
	}
	b.part = lines[len(lines)-1]
	if len(b.part) > 4096 { // a line without end (binary output, a progress bar)
		b.part = b.part[len(b.part)-4096:]
	}
	b.version++
	return len(p), nil
}

// Close ends the block with a status: "✓ status" or "✗ status". An empty
// status ends it as it is (a command the user edited before it ran).
func (b *ToolBlock) Close(ok bool, status string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed, b.ok, b.status = true, ok, status
	b.version++
}

func (b *ToolBlock) Lines(width int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.c.width == width && b.c.version == b.version {
		return b.c.lines
	}
	box := (*toolBox)(nil)
	if p := termenv.ColorProfile(); T.ToolBg && p <= termenv.ANSI256 {
		box = &toolBox{profile: p, width: width}
	}
	room := width - 3
	if box != nil {
		room = width - 2
	}

	var title []string
	for i, l := range strings.Split(strings.TrimRight(b.title, "\n"), "\n") {
		if i > 0 {
			l = "  " + l
		}
		for _, r := range strings.Split(ansi.Wrap(Safe(l), max(room, 10), ""), "\n") {
			title = append(title, T.ToolTitle.Render(r))
		}
	}
	if b.note != "" {
		note := T.Dim.Render(Safe(b.note))
		if last := len(title) - 1; lipgloss.Width(title[last]+" "+note) <= room {
			title[last] += " " + note
		} else {
			title = append(title, note)
		}
	}

	body := append([]string(nil), b.preview...)
	tail := b.tail
	if b.part != "" {
		tail = append(append([]string(nil), tail...), b.part)[max(0, len(tail)+1-tailRows):]
	}
	shown := b.total
	if b.part != "" {
		shown++
	}
	if hidden := shown - len(tail); hidden > 0 {
		body = append(body, T.Dim.Render(fmt.Sprintf("… %d earlier %s", hidden, plural(hidden, "line"))))
	}
	for _, l := range tail {
		body = append(body, T.ToolOutput.Render(ansi.Truncate(cleanLine(l), room, "…")))
	}
	if b.closed && b.status != "" {
		glyph := T.Success.Render("✓")
		if !b.ok {
			glyph = T.Error.Render("✗")
		}
		body = append(body, glyph+" "+T.Dim.Render(Safe(b.status)))
	}

	var lines []string
	if box == nil {
		lines = title
		for _, l := range body {
			lines = append(lines, ansi.Truncate("  "+l, width, "…"))
		}
	} else {
		c := colToolBg
		switch {
		case b.closed && b.status != "" && b.ok:
			c = colToolOkBg
		case b.closed && b.status != "":
			c = colToolFailBg
		}
		bg := box.seq(c)
		lines = append(lines, box.row(bg, ""))
		for _, l := range append(title, body...) {
			lines = append(lines, box.row(bg, l))
		}
		lines = append(lines, box.row(bg, ""))
	}
	b.c.width, b.c.version, b.c.lines = width, b.version, lines
	return lines
}
