//go:build !windows

package docsgpt

import "syscall"

// A refused and a reset connection, as the network calls report them.
var (
	errRefused error = syscall.ECONNREFUSED
	errReset   error = syscall.ECONNRESET
)
