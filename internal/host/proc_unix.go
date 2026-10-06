//go:build !windows

package host

import (
	"os"
	"os/exec"
	"syscall"
)

// newGroup starts cmd in a session of its own: a process group the daemon
// can signal as a whole, so the children of `sh -c` stop with it, and no
// controlling terminal, so a prompt (sudo, ssh) fails at once instead of
// waiting for a person who is not there.
func newGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// stopGroup asks the command's process group to stop (SIGTERM), or kills it
// (SIGKILL). A group that is already gone is not an error.
func stopGroup(cmd *exec.Cmd, kill bool) {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

// lockFile takes an exclusive lock on f without waiting, so two daemons
// never share one spool. The lock ends with the process (or an exec).
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
