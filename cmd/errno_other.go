//go:build !windows

package cmd

import "syscall"

// A refused and a reset connection, as the network calls report them.
var (
	errRefused error = syscall.ECONNREFUSED
	errReset   error = syscall.ECONNRESET
)
