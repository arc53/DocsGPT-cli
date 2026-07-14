//go:build !windows

package update

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the worker in its own session so it survives the parent
// command exiting.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
