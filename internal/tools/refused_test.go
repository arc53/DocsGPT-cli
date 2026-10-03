package tools

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// choosingUI answers every approval with choice (or err).
type choosingUI struct {
	choice string
	err    error
}

func (choosingUI) Open(string, string)                {}
func (choosingUI) Lines([]string)                     {}
func (choosingUI) Output() io.Writer                  { return io.Discard }
func (choosingUI) Close(bool, string)                 {}
func (u choosingUI) Choose([]ui.Item) (string, error) { return u.choice, u.err }
func (choosingUI) Edit(_, v string) (string, error)   { return v, nil }

// TestRefused keeps Refused in step with what Handle returns.
func TestRefused(t *testing.T) {
	call := docsgpt.ToolCall{Function: docsgpt.FunctionCall{Name: "run_command", Arguments: `{"command":"true"}`}}
	for _, tc := range []struct {
		ui      choosingUI
		refused bool
	}{
		{choosingUI{choice: "deny"}, true},
		{choosingUI{err: ui.ErrCancelled}, true},
		{choosingUI{err: ui.ErrNotInteractive}, true},
		{choosingUI{choice: "approve"}, false},
	} {
		s := &Session{UI: tc.ui, Timeout: time.Minute}
		got := s.Handle(context.Background(), func() {}, call)
		if Refused(got) != tc.refused {
			t.Errorf("%+v: Refused(%q) = %v", tc.ui, got, !tc.refused)
		}
	}
}
