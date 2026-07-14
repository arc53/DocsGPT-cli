//go:build windows

package update

import "os"

// Windows has no exec(2). Exit non-zero so restart-on-failure supervisors
// (Task Scheduler RestartOnFailure) relaunch the just-updated binary; exit 0
// is reserved for graceful shutdowns like a revoke and would stay stopped.
func Restart(string) error {
	os.Exit(3)
	return nil
}
