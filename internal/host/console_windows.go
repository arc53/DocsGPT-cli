//go:build windows

package host

import "syscall"

// DetachConsole frees the console window Windows attaches to the daemon
// when Task Scheduler starts it in the interactive session. Output must
// already be redirected to the log file (EnterServiceMode does both, in
// that order); without this call a console window would sit on the user's
// desktop for the daemon's whole lifetime.
func DetachConsole() {
	// FreeConsole failing (e.g. no console attached) is harmless.
	_, _, _ = syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole").Call()
}
