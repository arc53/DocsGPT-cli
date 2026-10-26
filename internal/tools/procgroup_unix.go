//go:build !windows

package tools

import (
	"context"
	"os/exec"
	"syscall"
)

// shellCommand runs command with sh -c.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}

// killProcessGroup starts cmd in a session of its own, without a
// controlling terminal, and makes cancellation kill its whole process
// group, so the children of `sh -c` die too (and stop holding the output
// pipe open). Without a terminal, anything that asks the user (sudo, ssh,
// read </dev/tty) fails at once rather than stopping in the background
// until the timeout while the user's typing goes to the chat.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
