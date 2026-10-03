package ui

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"

	"github.com/arc53/DocsGPT-cli/internal/attach"
)

// Block is one item of a Screen's transcript (a message, an answer, a tool
// call). It renders itself in width columns, as lines no wider, and may
// change while shown (a streaming answer): Screen.Changed redraws it.
type Block interface{ Lines(width int) []string }

// Prompt marks the blocks Ctrl+↑ and Ctrl+↓ move between: the user's
// messages.
type Prompt interface{ Prompt() }

// Expander is a block that shows part of what it holds (a command's
// output) unless expanded; Ctrl+O expands or collapses them all. Folds
// reports whether collapsing it hides anything.
type Expander interface {
	Expand(on bool)
	Folds() bool
}

// ScreenOptions configures a Screen.
type ScreenOptions struct {
	Commands []Command // the editor's slash commands
	History  *History
	Mouse    bool // scroll with the wheel and select text to copy it
	// EscEsc is sent as a message (a command) for Esc pressed twice on an
	// empty input while nothing runs; "" for nothing.
	EscEsc string
	// Headless keeps the blocks without a terminal, prompts failing with
	// ErrNotInteractive (for tests).
	Headless bool
}

// Screen is the chat on the alternate screen: the transcript, which it
// scrolls itself, above a status row, a panel for prompts, the editor and
// its footer, which stay at the bottom while answers stream in. Run it in
// one goroutine and drive it from another: every method but Run and
// Transcript is for that one.
type Screen struct {
	m        *screenModel
	p        *tea.Program
	headless bool
	mu       sync.Mutex // the model, when headless
	done     chan struct{}
}

const (
	frameInterval = 33 * time.Millisecond  // redraws for Changed, ~30fps
	panelGrace    = 300 * time.Millisecond // see panel.until
	syncStart     = "\x1b[?2026h"
	syncEnd       = "\x1b[?2026l"
)

// NewScreen sets up a Screen; Run shows it.
func NewScreen(o ScreenOptions) *Screen {
	m := &screenModel{ed: newEditorModel(o.Commands, o.History), follow: true, mouse: o.Mouse, width: 80, height: 24, escCmd: o.EscEsc}
	s := &Screen{m: m, headless: o.Headless, done: make(chan struct{})}
	if o.Headless {
		return s
	}
	if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
		m.width, m.height = w, h
	}
	m.out = &termOutput{File: os.Stdout}
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithOutput(m.out), tea.WithoutSignalHandler(), tea.WithReportFocus()}
	if o.Mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	if in := newTTYInput(); in != nil {
		in.blurred = &m.blurred
		opts, m.keys = append(opts, tea.WithInput(in)), true
	}
	s.p = tea.NewProgram(m, opts...)
	return s
}

// Run shows the screen until the user quits, Quit is called or a TERM or
// HUP signal comes (returned as a Signal), then restores the terminal. A
// Ctrl+C sent as a signal (kill -INT) interrupts like the key.
func (s *Screen) Run() error {
	defer close(s.done)
	if s.headless || !Interactive() {
		return ErrNotInteractive
	}
	// Resolve the adaptive colours now: the first Render would otherwise
	// query the terminal background while the program owns stdin.
	lipgloss.HasDarkBackground()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	got, ended := make(chan Signal, 1), make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigs:
				if sig == os.Interrupt {
					s.do(func(m *screenModel) tea.Cmd { m.interrupt(); return nil })
					continue
				}
				got <- Signal{sig}
				s.p.Quit()
				return
			case <-ended:
				return
			}
		}
	}()
	_, err := s.p.Run()
	close(ended)
	if s.m.keys {
		os.Stdout.WriteString(keysOff)
	}
	if s.m.titled {
		os.Stdout.WriteString(titlePop)
	}
	select {
	case sig := <-got:
		return sig
	default:
	}
	return err
}

// Transcript renders the transcript in width columns, for printing once Run
// has returned.
func (s *Screen) Transcript(width int) string {
	m := s.m
	m.width = width
	var b strings.Builder
	for i, lines := range m.spans() {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, l := range lines {
			if strings.Contains(l, "\x1b") {
				l += "\x1b[0m"
			}
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// do runs f in the program's goroutine, where the model lives.
func (s *Screen) do(f func(m *screenModel) tea.Cmd) {
	if s.headless {
		s.mu.Lock()
		defer s.mu.Unlock()
		f(s.m)
		return
	}
	select {
	case <-s.done:
	default:
		s.p.Send(doMsg(f))
	}
}

type doMsg func(m *screenModel) tea.Cmd

// Add appends a block to the transcript; an Expander as the others are.
func (s *Screen) Add(b Block) {
	s.do(func(m *screenModel) tea.Cmd {
		if e, ok := b.(Expander); ok {
			e.Expand(m.expanded)
		}
		m.blocks = append(m.blocks, b)
		return nil
	})
}

// Clear empties the transcript.
func (s *Screen) Clear() {
	s.do(func(m *screenModel) tea.Cmd { m.blocks, m.top, m.follow, m.sel = nil, 0, true, nil; return nil })
}

// Changed redraws the blocks that changed, within a frame's time.
func (s *Screen) Changed() {
	if s.headless || !s.m.redraw.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(frameInterval, func() {
		s.do(func(m *screenModel) tea.Cmd { m.redraw.Store(false); return nil })
	})
}

// Busy shows a spinner with label in the status row ("" for none), and
// makes Esc and Ctrl+C call cancel (nil for nothing to cancel).
func (s *Screen) Busy(label string, cancel func()) {
	s.do(func(m *screenModel) tea.Cmd { m.cancel = cancel; return m.setLabel(label) })
}

// Status changes the spinner's label, "" stopping it.
func (s *Screen) Status(label string) {
	s.do(func(m *screenModel) tea.Cmd { return m.setLabel(label) })
}

// Footer sets the line under the editor: left and right parts.
func (s *Screen) Footer(left, right string) {
	s.do(func(m *screenModel) tea.Cmd { m.ed.footer, m.ed.status = left, right; return nil })
}

// Mouse turns the wheel and selecting on or off.
func (s *Screen) Mouse(on bool) {
	s.do(func(m *screenModel) tea.Cmd {
		if m.mouse == on {
			return nil
		}
		m.mouse, m.sel = on, nil
		if on {
			return tea.EnableMouseCellMotion
		}
		return tea.DisableMouse
	})
}

// Copy puts text on the clipboard (see Copy), safe to call from any
// goroutine.
func (s *Screen) Copy(text string) error {
	if s.m.out == nil {
		return Copy(text, io.Discard)
	}
	return Copy(text, s.m.out)
}

// Quit ends Run.
func (s *Screen) Quit() {
	if !s.headless {
		s.p.Quit()
	}
}

// Kill ends Run at once.
func (s *Screen) Kill() {
	if !s.headless {
		s.p.Kill()
	}
}

// Message is a message the user sent.
type Message struct {
	Text  string        // pastes expanded
	Shown string        // pastes as markers
	Files []attach.File // the files attached, in the order of their markers
}

// Next waits for the next message the user sends. ok is false once the
// screen is gone. Messages sent while the caller is busy wait in a queue.
func (s *Screen) Next() (msg Message, ok bool) {
	if s.headless {
		return Message{}, false
	}
	reply := make(chan Message, 1)
	s.do(func(m *screenModel) tea.Cmd {
		if len(m.queue) > 0 {
			reply <- m.queue[0].Message
			m.queue = m.queue[1:]
		} else {
			m.waiter = reply
		}
		return nil
	})
	select {
	case msg := <-reply:
		return msg, true
	case <-s.done:
		return Message{}, false
	}
}

// Select shows sel in the panel above the editor and returns the chosen
// item's value; Esc or Ctrl+C give ErrCancelled.
func (s *Screen) Select(sel Select) (string, error) {
	if len(sel.Items) == 0 {
		return "", fmt.Errorf("ui: select has no items")
	}
	return s.ask(&panel{sel: newSelectModel(sel)})
}

// Input asks for a line of text in the panel above the editor.
func (s *Screen) Input(in Input) (string, error) {
	return s.ask(&panel{in: newInputModel(in)})
}

func (s *Screen) ask(p *panel) (string, error) {
	r := s.answer(p)
	return r.value, r.err
}

func (s *Screen) answer(p *panel) result {
	if s.headless {
		return result{err: ErrNotInteractive}
	}
	p.reply = make(chan result, 1)
	s.do(func(m *screenModel) tea.Cmd { return m.open(p) })
	select {
	case r := <-p.reply:
		return r
	case <-s.done:
		return result{err: ErrCancelled}
	}
}

// open shows p in the panel.
func (m *screenModel) open(p *panel) tea.Cmd {
	if m.cancel != nil && time.Since(m.typed) < time.Second {
		p.until = time.Now().Add(panelGrace)
	}
	m.panel, m.follow = p, true
	return nil
}

type panel struct {
	sel   *selectModel
	in    *inputModel
	reply chan result
	// until: a prompt that opens while the user types (an approval
	// during an answer) takes keys only from then on; the ones before go
	// on into the editor rather than answer it.
	until time.Time
}

type result struct {
	value  string
	action string // the Select action that chose it
	err    error
}

type (
	spinMsg    struct{}
	resumedMsg struct{}
)

// queued is a message sent while the chat was busy, with the pastes its
// markers stand for.
type queued struct {
	Message
	pastes map[int]string
}

type screenModel struct {
	width, height int
	blocks        []Block
	ed            *editorModel
	panel         *panel

	top      int  // the first transcript line shown
	follow   bool // the view stays at the end of the transcript
	expanded bool // Expanders show all they hold (Ctrl+O)
	leftAt   int  // transcript lines when the view left the end
	total    int  // transcript lines, as last drawn
	viewRows int  // transcript rows, as last drawn
	wheel    time.Time
	typed    time.Time // the last key the editor got

	label    string // the spinner's, "" when idle
	spin     int
	spinning bool
	cancel   func()
	waiter   chan Message // the caller of Next, waiting
	queue    []queued     // messages sent while it was busy
	listed   bool         // the queue is listed above the editor, as last drawn

	mouse bool
	sel   *selection // the text selected with the mouse
	click struct {   // the last click, to count a double or triple one
		at   time.Time
		word [2]point
		n    int
	}
	flash   string // a notice in the status row ("Copied …")
	flashOK bool
	flashID int

	keys   bool // modified keys are asked for (not on Windows)
	out    *termOutput
	redraw atomic.Bool // Changed has a redraw on its way

	escCmd  string      // see ScreenOptions.EscEsc
	escAt   time.Time   // the first Esc of a pair
	title   string      // the terminal title set, "" for none
	titled  bool        // the terminal's own title is saved, to come back
	blurred atomic.Bool // the terminal reported losing focus
}

// Init asks for modified keys once on the alternate screen: kitty keeps
// the flags of each screen apart.
func (m *screenModel) Init() tea.Cmd {
	m.reclaim()
	return nil
}

// reclaim asks for the terminal's modes again after it was handed back
// (bubbletea restores neither the key reports nor the mouse).
func (m *screenModel) reclaim() tea.Cmd {
	if m.keys {
		m.out.WriteString(keysOn)
	}
	if m.mouse {
		return tea.EnableMouseCellMotion
	}
	return nil
}

func (m *screenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doMsg:
		return m, msg(m)
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonWheelUp && msg.Button != tea.MouseButtonWheelDown {
			return m, m.pointer(msg)
		}
		n := wheelLines(m.wheel)
		m.wheel = time.Now()
		if msg.Button == tea.MouseButtonWheelUp {
			n = -n
		}
		m.scroll(n)
		if m.sel != nil && m.sel.held {
			m.extend()
		}
	case dragTickMsg:
		return m, m.dragged()
	case copiedMsg:
		if msg.err != nil {
			return m, m.notify("Could not copy: "+msg.err.Error(), false)
		}
		return m, m.notify(fmt.Sprintf("Copied %d %s", msg.n, plural(msg.n, "character")), true)
	case openedMsg:
		if msg.err != nil {
			return m, m.notify("Could not open the link: "+msg.err.Error(), false)
		}
		return m, m.notify("Opened "+msg.url, true)
	case flashMsg:
		if msg.id == m.flashID {
			m.flash = ""
		}
	case tea.KeyMsg:
		return m, m.key(msg)
	case spinMsg:
		if m.label == "" {
			m.spinning = false
			return m, nil
		}
		m.spin++
		return m, spinTick()
	case resumedMsg:
		m.retitle(true)
		return m, m.reclaim()
	case editedMsg:
		m.retitle(false) // $EDITOR may have set its own
		cmd, _ := m.ed.update(msg)
		return m, tea.Batch(cmd, m.reclaim())
	case tea.FocusMsg, tea.BlurMsg: // where ttyInput does not take them
		_, blur := msg.(tea.BlurMsg)
		m.blurred.Store(blur)
	case hintMsg:
		m.ed.update(msg)
	case clipMsg:
		cmd, _ := m.ed.update(msg)
		return m, tea.Batch(cmd, m.editorNotice())
	case tickMsg, validatedMsg:
		if p := m.panel; p != nil && p.in != nil {
			_, cmd := p.in.Update(msg)
			return m, m.settle(cmd)
		}
	}
	return m, nil
}

// notify shows a notice in the status row for a while: 2s, 5s when it is
// an error.
func (m *screenModel) notify(text string, ok bool) tea.Cmd {
	m.flashID++
	m.flash, m.flashOK = text, ok
	wait := 2 * time.Second
	if !ok {
		wait = 5 * time.Second
	}
	id := m.flashID
	return tea.Tick(wait, func(time.Time) tea.Msg { return flashMsg{id} })
}

// perLineWheel: a terminal on macOS reports a wheel event per line, the
// system having accelerated the wheel already (pi's finding); elsewhere an
// event is a notch.
var perLineWheel = runtime.GOOS == "darwin" && os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_TTY") == ""

// wheelLines is how far a wheel event scrolls, given when the last came: a
// line where events are per line or come in a burst (a notch reported as
// several, a high-resolution wheel), else 3.
func wheelLines(last time.Time) int {
	if perLineWheel || time.Since(last) < 5*time.Millisecond {
		return 1
	}
	return 3
}

func spinTick() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return spinMsg{} })
}

func (m *screenModel) setLabel(label string) tea.Cmd {
	m.label = label
	if label == "" || m.spinning {
		return nil
	}
	m.spinning = true
	return spinTick()
}

func (m *screenModel) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	switch s {
	case "ctrl+z":
		return m.suspend()
	case "ctrl+l":
		return tea.ClearScreen
	case "ctrl+o":
		return m.expand(!m.expanded)
	}
	if s == "esc" && m.sel != nil {
		m.sel = nil
		return nil
	}
	if m.scrollKey(s) {
		return nil
	}
	if p := m.panel; p != nil && time.Now().After(p.until) {
		return m.panelKey(k)
	}
	if s == "alt+up" && len(m.queue) > 0 {
		m.requeue() // to edit, the answer going on
		return nil
	}
	m.typed = time.Now()
	if (s == "ctrl+c" || s == "esc" && m.ed.popup == nil) && m.cancel != nil {
		m.interrupt()
		return nil
	}
	if m.escEsc(s) {
		return nil
	}
	cmd, act := m.ed.update(k)
	cmd = tea.Batch(cmd, m.editorNotice())
	switch act {
	case editSubmit:
		pastes := m.ed.pastes
		var msg Message
		msg.Text, msg.Shown, msg.Files = m.ed.take()
		m.follow = true
		if m.waiter != nil {
			m.waiter <- msg
			m.waiter = nil
		} else {
			m.queue = append(m.queue, queued{msg, pastes})
		}
	case editQuit:
		if m.cancel != nil {
			m.cancel()
		}
		m.close(result{err: ErrCancelled})
		return tea.Quit
	}
	return cmd
}

// editorNotice shows the editor's notice (a file it could not attach), if
// any, in the status row.
func (m *screenModel) editorNotice() tea.Cmd {
	n := m.ed.notice
	if n == "" {
		return nil
	}
	m.ed.notice = ""
	return m.notify(n, false)
}

// expand expands or collapses the Expanders, keeping a scrolled-back view
// in place: on the block at its top, from that block's start when it is
// one that changed.
func (m *screenModel) expand(on bool) tea.Cmd {
	block, line := -1, 0
	if !m.follow {
		block, line, _ = m.locate(m.top)
	}
	m.expanded = on
	for i, b := range m.blocks {
		if e, ok := b.(Expander); ok {
			e.Expand(on)
			if i == block && e.Folds() {
				line = 0
			}
		}
	}
	if block >= 0 {
		m.top = m.offset(block) + min(line, max(0, len(m.blocks[block].Lines(m.width))-1))
		m.leftAt = -1 // the lines are counted anew: none is new
	}
	m.sel = nil
	if on {
		return m.notify("Tool output expanded", true)
	}
	return m.notify("Tool output collapsed", true)
}

// interrupt cancels what is running. Messages queued meanwhile go back to
// the editor rather than out.
func (m *screenModel) interrupt() {
	if m.cancel == nil {
		return
	}
	m.cancel()
	m.cancel = nil
	m.requeue()
}

// requeue puts the queued messages back into the editor, before what it
// holds, a blank line apart; their pastes stay collapsed and their files
// attached, renumbered.
func (m *screenModel) requeue() {
	if len(m.queue) == 0 {
		return
	}
	var texts []string
	pastes, files := map[int]string{}, map[int]attach.File{}
	add := func(shown string, from map[int]string, attached []attach.File) {
		shown = PasteMarker.ReplaceAllStringFunc(shown, func(marker string) string {
			sub := PasteMarker.FindStringSubmatch(marker)
			id, _ := strconv.Atoi(sub[1])
			p, ok := from[id]
			if !ok {
				return marker
			}
			n := len(pastes) + 1
			pastes[n] = p
			return "[paste #" + strconv.Itoa(n) + marker[len("[paste #"+sub[1]):]
		})
		for _, f := range attached {
			old := f.Marker()
			f.ID = len(files) + 1
			files[f.ID] = f
			shown = strings.Replace(shown, old, "\x00"+f.Marker(), 1) // not renumbered again
		}
		texts = append(texts, strings.ReplaceAll(shown, "\x00", ""))
	}
	for _, q := range m.queue {
		add(q.Shown, q.pastes, q.Files)
	}
	if !m.ed.empty() {
		add(m.ed.text(), m.ed.pastes, m.ed.attached())
	}
	m.ed.replace(strings.Join(texts, "\n\n"), pastes, files)
	m.queue = nil
}

// scrollKey scrolls the transcript for s and reports whether it did.
// Home and End do only with nothing typed, and PgUp/PgDn not while a list
// is open.
func (m *screenModel) scrollKey(s string) bool {
	list := m.panel != nil && m.panel.sel != nil && !m.panel.sel.inline()
	free := m.panel == nil && m.ed.empty()
	switch {
	case s == "pgup" && !list:
		m.scroll(-max(1, m.viewRows-1))
	case s == "pgdown" && !list:
		m.scroll(max(1, m.viewRows-1))
	case s == "shift+up":
		m.scroll(-1)
	case s == "shift+down":
		m.scroll(1)
	case s == "ctrl+home", s == "home" && free:
		m.scroll(-m.total)
	case s == "ctrl+end", s == "end" && free:
		m.follow = true
	case s == "ctrl+up":
		m.jump(-1)
	case s == "ctrl+down":
		m.jump(1)
	default:
		return false
	}
	return true
}

// scroll moves the view n lines down (up when negative); the view follows
// the transcript again once it reaches the end.
func (m *screenModel) scroll(n int) {
	end := max(0, m.total-m.viewRows)
	if m.follow {
		m.top, m.leftAt = end, m.total
	}
	m.top = clamp(m.top+n, 0, end)
	m.follow = m.top == end
}

// jump scrolls to the previous (dir -1) or next user message.
func (m *screenModel) jump(dir int) {
	top := m.top
	if m.follow {
		top = max(0, m.total-m.viewRows)
	}
	target := -1
	at := 0
	for _, b := range m.blocks {
		n := len(b.Lines(m.width))
		if n == 0 {
			continue
		}
		if at > 0 {
			at++
		}
		if _, ok := b.(Prompt); ok && (dir < 0 && at < top || dir > 0 && at > top && target < 0) {
			target = at
		}
		at += n
	}
	if target < 0 {
		if dir > 0 {
			m.follow = true
		}
		return
	}
	m.scroll(target - top)
}

func (m *screenModel) panelKey(k tea.KeyMsg) tea.Cmd {
	p := m.panel
	if p.sel != nil {
		p.sel.key(k)
		if p.sel.done {
			r := result{err: ErrCancelled}
			if p.sel.chosen != nil {
				r = result{value: p.sel.chosen.value(), action: p.sel.action}
			}
			m.close(r)
		}
		return nil
	}
	_, cmd := p.in.Update(k)
	return m.settle(cmd)
}

// settle closes an input panel that is done, else passes its command on.
func (m *screenModel) settle(cmd tea.Cmd) tea.Cmd {
	in := m.panel.in
	if !in.done {
		return cmd
	}
	r := result{err: ErrCancelled}
	if in.submitted {
		r = result{value: in.field.String()}
	}
	m.close(r)
	return nil
}

// close closes the panel, if any, with r for its caller.
func (m *screenModel) close(r result) {
	if m.panel != nil {
		m.panel.reply <- r
		m.panel = nil
	}
}

// resize keeps the line at the top of a scrolled-back view in place: the
// same block, as far down it.
func (m *screenModel) resize(w, h int) {
	m.sel = nil
	if w != m.width && !m.follow && m.width > 0 {
		block, line, n := m.locate(m.top)
		m.width = w
		if block >= 0 {
			m.top = m.offset(block) + line*len(m.blocks[block].Lines(w))/max(1, n)
		}
		m.leftAt = -1 // the lines are counted anew: none is new
	}
	m.width, m.height = w, h
	m.ed.setSize(w, h)
}

// locate finds transcript line i: the block, the line in it and the
// block's height (block -1 when none).
func (m *screenModel) locate(i int) (block, line, n int) {
	at := 0
	for k, b := range m.blocks {
		l := len(b.Lines(m.width))
		if l == 0 {
			continue
		}
		if at > 0 {
			at++ // the blank line between blocks
		}
		if i < at+l {
			return k, max(0, i-at), l
		}
		at += l
	}
	return -1, 0, 0
}

// offset returns the first transcript line of block k.
func (m *screenModel) offset(k int) int {
	at := 0
	for _, b := range m.blocks[:k] {
		if l := len(b.Lines(m.width)); l > 0 {
			at += l + 1
		}
	}
	return at
}

// spans returns the lines of the blocks that show something.
func (m *screenModel) spans() [][]string {
	var spans [][]string
	for _, b := range m.blocks {
		if l := b.Lines(m.width); len(l) > 0 {
			spans = append(spans, l)
		}
	}
	return spans
}

func (m *screenModel) View() string {
	w, h := m.width, m.height
	// A panel gets the room it needs, short of the status row and the
	// footer; the editor's text goes first when the window is too short.
	var panel []string
	if p := m.panel; p != nil {
		room := max(1, h-2)
		var v string
		if p.sel != nil {
			p.sel.setSize(w, min(room, h/2+4)+1)
			v = p.sel.View()
		} else {
			p.in.setSize(w, h)
			v = p.in.View()
		}
		panel = strings.Split(v, "\n")
		panel = panel[clamp(len(panel)-room, 0, len(panel)):]
	}
	m.ed.setSize(w, h)
	queue := m.queueLines(w)
	if h-1-len(panel)-len(queue) < 8 {
		queue = nil // the editor and some transcript first; the status row counts them
	}
	m.listed = queue != nil
	rest := h - 1 - len(panel) - len(queue)
	editor := m.ed.view(w, max(4, rest-1), m.panel == nil)
	if m.panel != nil && rest < len(editor) {
		editor = editor[len(editor)-1:]
	}
	rows := max(0, rest-len(editor))

	spans := m.spans()
	total := 0
	for i, s := range spans {
		total += len(s)
		if i > 0 {
			total++
		}
	}
	m.total, m.viewRows = total, rows
	if m.leftAt < 0 {
		m.leftAt = total
	}
	end := max(0, total-rows)
	if m.follow {
		m.top = end
	} else if m.top = clamp(m.top, 0, end); m.top == end {
		m.follow = true
	}

	lines := make([]string, 0, h)
	lines = append(lines, window(spans, m.top, rows)...)
	if s := m.sel; s != nil && !s.empty() {
		for r := range lines {
			if i := m.top + r; i >= s.from.line && i <= s.to.line {
				a, b := 0, endCol
				if i == s.from.line {
					a = s.from.col
				}
				if i == s.to.line {
					b = s.to.col
				}
				lines[r] = highlight(lines[r], a, b)
			}
		}
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	lines = append(lines, m.statusLine(w))
	lines = append(lines, queue...)
	lines = append(lines, panel...)
	lines = append(lines, editor...)
	return strings.Join(lines[max(0, len(lines)-h):], "\n")
}

// window returns rows lines of the transcript from line top, the blocks a
// blank line apart.
func window(spans [][]string, top, rows int) []string {
	out := make([]string, 0, rows)
	at := 0
	for i, lines := range spans {
		if i > 0 {
			if at >= top && len(out) < rows {
				out = append(out, "")
			}
			at++
		}
		if at+len(lines) > top {
			for _, l := range lines[max(0, top-at):] {
				if len(out) == rows {
					return out
				}
				out = append(out, l)
			}
		}
		at += len(lines)
		if len(out) == rows {
			break
		}
	}
	return out
}

// queueLines lists the messages waiting to be sent, a row each (the first
// three), and how to edit them.
func (m *screenModel) queueLines(width int) []string {
	if len(m.queue) == 0 {
		return nil
	}
	dim := fg(Colors.Dim)
	var out []string
	for _, q := range m.queue[:min(3, len(m.queue))] {
		out = append(out, dim.Render(ansi.Truncate("Queued: "+strings.Join(strings.Fields(q.Shown), " "), width, "…")))
	}
	hint := "↳ alt+↑ to edit"
	if n := len(m.queue) - 3; n > 0 {
		hint = fmt.Sprintf("↳ %d more · alt+↑ to edit", n)
	}
	return append(out, dim.Render(ansi.Truncate(hint, width, "…")))
}

// statusLine is the row above the queue, the panel and the editor: the
// spinner while something runs, and how much is below a scrolled-back
// view.
func (m *screenModel) statusLine(width int) string {
	dim := fg(Colors.Dim)
	var left []string
	if m.label != "" && m.panel == nil {
		s := fg(Colors.Accent).Render(spinnerFrames[m.spin%len(spinnerFrames)]) + " " + fg(Colors.Muted).Render(m.label)
		if m.cancel != nil {
			s += dim.Render(" · esc to stop")
		}
		left = append(left, s)
	}
	if n := len(m.queue); n > 0 && !m.listed {
		left = append(left, dim.Render(fmt.Sprintf("%d queued · alt+↑ to edit", n)))
	}
	if m.flash != "" {
		style := dim
		if !m.flashOK {
			style = fg(Colors.Error)
		}
		left = append(left, style.Render(m.flash))
	}
	l := strings.Join(left, dim.Render(" · "))
	below := m.total - m.top - m.viewRows
	if m.follow || below <= 0 {
		return ansi.Truncate(l, width, "…")
	}
	what := fmt.Sprintf("↓ %d more %s", below, plural(below, "line"))
	if n := m.total - m.leftAt; n > 0 {
		what = fmt.Sprintf("↓ %d new %s", n, plural(n, "line"))
	}
	key := "end"
	if m.panel != nil || !m.ed.empty() {
		key = "ctrl+end"
	}
	r := fg(Colors.Accent).Render(what) + dim.Render(" · "+key+" to jump")
	room := width - lipgloss.Width(r) - 2
	if room < 1 {
		return ansi.Truncate(r, width, "…")
	}
	l = ansi.Truncate(l, room, "…")
	return l + strings.Repeat(" ", width-lipgloss.Width(l)-lipgloss.Width(r)) + r
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// handoff runs a function with the terminal handed back: bubbletea has left
// the alternate screen and restored the terminal's mode. The modified-key
// reports are off meanwhile; reclaim asks for them again.
type handoff func() error

func (h handoff) Run() error {
	if runtime.GOOS != "windows" {
		os.Stdout.WriteString(keysOff)
	}
	return h()
}

func (handoff) SetStdin(io.Reader)  {}
func (handoff) SetStdout(io.Writer) {}
func (handoff) SetStderr(io.Writer) {}

// suspend stops the process (Ctrl+Z) with the terminal restored, and takes
// it back on fg.
func (m *screenModel) suspend() tea.Cmd {
	if suspendProcess == nil {
		return nil
	}
	return tea.Exec(handoff(func() error {
		if m.titled { // the shell's title while stopped
			os.Stdout.WriteString(titlePop)
			m.titled = false
		}
		suspendProcess()
		return nil
	}), func(error) tea.Msg { return resumedMsg{} })
}

// termOutput is stdout for a Screen: every write is one synchronized
// update, so the terminal never shows half a frame.
type termOutput struct {
	*os.File
	mu sync.Mutex
}

func (o *termOutput) Write(p []byte) (int, error) { return o.WriteString(string(p)) }

func (o *termOutput) WriteString(s string) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, err := o.File.WriteString(syncStart + s + syncEnd); err != nil {
		return 0, err
	}
	return len(s), nil
}
