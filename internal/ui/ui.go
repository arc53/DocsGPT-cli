// Package ui holds the interactive components: the small inline prompts
// (select lists, confirmations, text inputs, a spinner) used wherever the
// CLI asks the user something, which render inline and collapse to a
// one-line summary once answered, and the chat's full-screen Screen, which
// shows the same prompts in a panel.
package ui

import (
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

var (
	// ErrCancelled is returned when the user dismisses a prompt (Esc, Ctrl+C).
	ErrCancelled = errors.New("cancelled")
	// ErrNotInteractive is returned when stdin or stdout is not a terminal;
	// callers fall back to flags or environment variables.
	ErrNotInteractive = errors.New("not an interactive terminal")
)

// Signal is the error of a prompt that a TERM or HUP signal ended (kill,
// timeout, a closed terminal): the caller stops too, and exits with 128 +
// the signal.
type Signal struct{ os.Signal }

func (s Signal) Error() string { return s.String() }

// Palette holds the colours every component draws with. Set Colors to
// restyle them; lipgloss already honours NO_COLOR.
type Palette struct {
	Accent  lipgloss.TerminalColor // cursor, selected row, spinner
	Muted   lipgloss.TerminalColor // descriptions, hint actions, scroll info
	Dim     lipgloss.TerminalColor // hint keys, placeholders, separators
	Success lipgloss.TerminalColor
	Error   lipgloss.TerminalColor
	Border  lipgloss.TerminalColor
}

// Colors is the active palette (pi's dark/light tones by default).
var Colors = Palette{
	Accent:  lipgloss.AdaptiveColor{Dark: "#a798d7", Light: "#7459b4"},
	Muted:   lipgloss.AdaptiveColor{Dark: "#9da5a9", Light: "#677176"},
	Dim:     lipgloss.AdaptiveColor{Dark: "#7e888e", Light: "#879095"},
	Success: lipgloss.AdaptiveColor{Dark: "#68b78d", Light: "#337e58"},
	Error:   lipgloss.AdaptiveColor{Dark: "#ea7f81", Light: "#c8253d"},
	Border:  lipgloss.AdaptiveColor{Dark: "#768186", Light: "#9aa2a7"},
}

func fg(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Text attributes are written directly: lipgloss drops them together with
// the colours under NO_COLOR, which would hide the input cursor.
func bold(s string) string      { return "\x1b[1m" + s + "\x1b[22m" }
func reverse(s string) string   { return "\x1b[7m" + s + "\x1b[27m" }
func underline(s string) string { return "\x1b[4m" + s + "\x1b[24m" }

// Interactive reports whether prompts can run: stdin and stdout are terminals.
func Interactive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// sizer is implemented by every model so it can lay out its first frame at
// the real terminal width (the first WindowSizeMsg arrives after it).
type sizer interface{ setSize(w, h int) }

// Prompter shows prompts: Inline, or in a Screen's panel.
type Prompter interface {
	Select(Select) (string, error)
	Input(Input) (string, error)
}

// Inline shows prompts inline (Select.Run, Input.Run).
var Inline Prompter = inline{}

type inline struct{}

func (inline) Select(s Select) (string, error) { return s.Run() }
func (inline) Input(in Input) (string, error)  { return in.Run() }

// run drives one prompt inline on stdout, or on stderr when toStderr is
// set, and returns its final model.
func run(m interface {
	tea.Model
	sizer
}, toStderr bool) (tea.Model, error) {
	out := os.Stdout
	if toStderr {
		out = os.Stderr
	}
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(out.Fd()) {
		return nil, ErrNotInteractive
	}
	// Resolve the adaptive colours now: the first Render would otherwise
	// query the terminal background while the program owns stdin.
	lipgloss.HasDarkBackground()
	if w, h, err := term.GetSize(out.Fd()); err == nil {
		m.setSize(w, h)
	}
	// bubbletea would end on TERM as if the prompt were answered, and leave
	// HUP to kill the process in raw mode; both stop the prompt instead.
	p := tea.NewProgram(m, tea.WithOutput(out), tea.WithoutSignalHandler())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	got, done := make(chan os.Signal, 1), make(chan struct{})
	go func() {
		select {
		case sig := <-sigs:
			got <- sig
			p.Kill()
		case <-done:
		}
	}()
	final, err := p.Run()
	close(done)
	select {
	case sig := <-got:
		if sig != os.Interrupt {
			return nil, Signal{sig}
		}
		return nil, ErrCancelled
	default:
	}
	if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
		return nil, ErrCancelled
	}
	return final, err
}

// summary is the frame a finished prompt leaves behind. The trailing newline
// keeps it: bubbletea erases the line the cursor ends on when it stops. An
// empty summary still needs a blank frame, or the last frame would stay.
func summary(s string) string {
	if s == "" {
		return " "
	}
	return s + "\n"
}

// answered is the default summary: "Title: answer", or "Title? answer".
func answered(title, answer string) string {
	if title == "" {
		return fg(Colors.Accent).Render(answer)
	}
	sep := ": "
	if strings.HasSuffix(title, "?") || strings.HasSuffix(title, ":") {
		sep = " "
	}
	return title + sep + fg(Colors.Accent).Render(answer)
}

// hints renders "key action · key action" from key/action pairs, dropping
// trailing pairs that do not fit in width.
func hints(width int, pairs ...string) string {
	key, action, sep := fg(Colors.Dim), fg(Colors.Muted), fg(Colors.Dim).Render(" · ")
	out := ""
	for i := 0; i+1 < len(pairs); i += 2 {
		next := key.Render(pairs[i]) + " " + action.Render(pairs[i+1])
		if out != "" {
			next = out + sep + next
		}
		if lipgloss.Width(next) > width {
			break
		}
		out = next
	}
	return out
}

// frame joins a prompt's lines, cutting any that would wrap.
func frame(lines []string, width int) string {
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return strings.Join(lines, "\n")
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }
