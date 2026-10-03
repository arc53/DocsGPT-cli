package tools

import (
	"io"

	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/ui"
)

// UI shows tool calls and asks the user about them: on stderr by default
// (ask), in the transcript and panel of the chat's screen there.
type UI interface {
	// Open starts the block of a call; another Open before Close ends
	// the first as it is (a command the user edited).
	Open(title, note string)
	Lines(lines []string) // preview lines, safe already
	Output() io.Writer    // a running command's output, until Close
	Close(ok bool, status string)
	Choose(items []ui.Item) (string, error) // ui.ErrCancelled on Esc or Ctrl+C
	Edit(title, value string) (string, error)
}

// stderrUI draws the blocks on stderr and asks inline there.
type stderrUI struct{ view *display.TailView }

func (*stderrUI) Open(title, note string) { display.ToolTitle(title, note) }
func (*stderrUI) Lines(lines []string)    { display.ToolLines(lines) }

func (u *stderrUI) Output() io.Writer {
	u.view = display.NewTailView()
	return u.view
}

func (u *stderrUI) Close(ok bool, status string) {
	if u.view != nil {
		u.view.Close()
		u.view = nil
	}
	display.ToolStatus(ok, status)
}

func (*stderrUI) Choose(items []ui.Item) (string, error) {
	return ui.Select{Items: items, Inline: true, Stderr: true, Summary: func(ui.Item) string { return "" }}.Run()
}

func (*stderrUI) Edit(title, value string) (string, error) {
	return ui.Input{Title: title, Value: value, Stderr: true, Summary: func(string) string { return "" }}.Run()
}
