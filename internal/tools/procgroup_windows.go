package tools

import (
	"os/exec"
	"strconv"
	"syscall"
)

// killProcessGroup gives cmd a hidden console of its own, so a program that
// reads the console directly (a password prompt) cannot take the user's
// keystrokes, and makes cancellation kill the whole process tree.
func killProcessGroup(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	cmd.Cancel = func() error {
		if exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run() == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
