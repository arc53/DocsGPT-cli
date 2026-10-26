package docsgpt

import "syscall"

// A refused and a reset connection, as the network calls report them:
// Winsock codes, which syscall.ECONNREFUSED and ECONNRESET do not match.
var (
	errRefused error = syscall.Errno(10061) // WSAECONNREFUSED
	errReset   error = syscall.WSAECONNRESET
)
