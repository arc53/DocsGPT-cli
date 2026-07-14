//go:build windows

package update

import (
	"os/exec"
	"syscall"
)

const (
	// syscall exposes CREATE_NEW_PROCESS_GROUP but not DETACHED_PROCESS.
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// detachProcess detaches the worker from the parent's console and process
// group so it survives the parent command exiting.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNewProcessGroup,
	}
}
