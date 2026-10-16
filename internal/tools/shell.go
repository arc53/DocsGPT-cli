package tools

import (
	"context"
	"time"
)

// RunShell runs a command the user typed in the chat ("!cmd"), shown with
// show like the agent's commands but without asking and without a time
// limit. It returns the output and exit status, for the model, and
// whether the command succeeded (exit 0).
func RunShell(ctx context.Context, show UI, command string) (out string, ok bool) {
	if show == nil {
		show = &stderrUI{}
	}
	ui := &closeUI{UI: show}
	s := &Session{AutoApprove: true, Timeout: 100 * 365 * 24 * time.Hour, UI: ui}
	return s.runCommand(ctx, func() {}, command, "", 0), ui.ok
}

// closeUI is a UI that keeps how its last block closed.
type closeUI struct {
	UI
	ok bool
}

func (u *closeUI) Close(ok bool, status string) {
	u.ok = ok
	u.UI.Close(ok, status)
}
