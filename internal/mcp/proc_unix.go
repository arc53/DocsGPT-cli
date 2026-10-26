//go:build !windows

package mcp

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd in a session of its own: no controlling terminal (a
// server cannot read the user's keys), and a process group to end whole.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func terminate(cmd *exec.Cmd) { syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }

func kill(cmd *exec.Cmd) { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

// killOrphans kills what is left of the group once the server ended.
func killOrphans(cmd *exec.Cmd) { kill(cmd) }
