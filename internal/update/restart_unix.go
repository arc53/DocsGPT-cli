//go:build !windows

package update

import (
	"os"
	"syscall"
)

// Restart replaces the current process with the binary at exePath, keeping
// PID, args, and environment — service supervisors never see an exit, so
// their restart policy doesn't matter.
func Restart(exePath string) error {
	return syscall.Exec(exePath, os.Args, os.Environ())
}
