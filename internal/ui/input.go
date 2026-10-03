package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Input asks for one line of text.
type Input struct {
	Title       string
	Placeholder string
	Value       string // initial text
	Mask        bool   // render every character as • (keys, tokens)
	Stderr      bool   // draw on stderr, leaving stdout to the answer
	// Validate runs on Enter behind a spinner; an error is shown under the
	// field and the prompt stays open. ctx is cancelled if the user quits.
	Validate func(ctx context.Context, value string) error
	// Summary is the line left once submitted; it defaults to "Title: value"
	// (bullets when masked) and an empty result leaves nothing.
	Summary func(value string) string
}

// Run shows the prompt and returns the submitted text.
func (in Input) Run() (string, error) {
	m, err := run(newInputModel(in), in.Stderr)
	if err != nil {
		return "", err
	}
	im := m.(*inputModel)
	if !im.submitted {
		return "", ErrCancelled
	}
	return im.field.String(), nil
}

type inputModel struct {
	Input
	field      field
	width      int
	errMsg     string
	validating bool
	frame      int
	gen        int // validation run, so a stale tick chain stops
	cancel     context.CancelFunc
	done       bool
	submitted  bool
}

type (
	validatedMsg struct{ err error }
	tickMsg      struct{ gen int }
)

func newInputModel(in Input) *inputModel {
	m := &inputModel{Input: in, width: 80}
	m.field.set(in.Value)
	return m
}

func (m *inputModel) setSize(w, _ int) { m.width = w }

func (m *inputModel) Init() tea.Cmd { return nil }

func (m *inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tickMsg:
		if m.validating && msg.gen == m.gen {
			m.frame = (m.frame + 1) % len(spinnerFrames)
			return m, tick(m.gen)
		}
	case validatedMsg:
		m.cancel()
		m.validating = false
		if msg.err != nil {
			m.errMsg = strings.Join(strings.Fields(msg.err.Error()), " ")
			return m, nil
		}
		return m, m.finish(true)
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, m.finish(false)
		}
		if m.validating {
			return m, nil
		}
		if msg.String() == "enter" {
			if m.Validate == nil {
				return m, m.finish(true)
			}
			ctx, cancel := context.WithCancel(context.Background())
			m.cancel, m.validating, m.errMsg, m.frame = cancel, true, "", 0
			m.gen++
			validate, value := m.Validate, m.field.String()
			return m, tea.Batch(tick(m.gen), func() tea.Msg { return validatedMsg{validate(ctx, value)} })
		}
		if m.field.update(msg) {
			m.errMsg = ""
		}
	}
	return m, nil
}

func tick(gen int) tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return tickMsg{gen} })
}

func (m *inputModel) finish(ok bool) tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	m.done, m.submitted, m.validating = true, ok, false
	return tea.Quit
}

func (m *inputModel) View() string {
	if m.done {
		if !m.submitted {
			return summary("")
		}
		v := m.field.String()
		if m.Summary != nil {
			return summary(m.Summary(v))
		}
		if m.Mask && v != "" {
			v = "••••••••"
		}
		return summary(answered(m.Title, v))
	}
	var lines []string
	if m.Title != "" {
		lines = append(lines, bold(m.Title))
	}
	lines = append(lines, fg(Colors.Dim).Render("> ")+m.field.view(m.width-2, m.Mask, m.Placeholder))
	switch {
	case m.validating:
		lines = append(lines, fg(Colors.Accent).Render(spinnerFrames[m.frame])+" "+fg(Colors.Muted).Render("Validating…"))
	case m.errMsg != "":
		lines = append(lines, fg(Colors.Error).Render("✗ "+m.errMsg))
	default:
		lines = append(lines, hints(m.width, "enter", "submit", "esc", "cancel"))
	}
	return frame(lines, m.width)
}
