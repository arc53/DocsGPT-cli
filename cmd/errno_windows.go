package cmd

import "golang.org/x/sys/windows"

// A refused and a reset connection, as the network calls report them:
// Winsock codes, which syscall.ECONNREFUSED and ECONNRESET do not match.
var (
	errRefused error = windows.WSAECONNREFUSED
	errReset   error = windows.WSAECONNRESET
)
