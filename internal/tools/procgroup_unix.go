//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// killProcessGroup starts cmd in its own process group and makes
// cancellation kill the whole group, so the children of `sh -c` die too
// (and stop holding the output pipe open).
func killProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
