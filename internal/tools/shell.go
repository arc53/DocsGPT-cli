package tools

import (
	"context"
	"time"
)

// RunShell runs a command the user typed in the chat ("!cmd"), shown like
// the agent's commands but without asking and without a time limit. It
// returns the output and exit status, for the model.
func RunShell(ctx context.Context, command string) string {
	s := &Session{AutoApprove: true, Timeout: 100 * 365 * 24 * time.Hour}
	return s.runCommand(ctx, func() {}, command, "")
}
