//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package ui

import (
	"os"

	"golang.org/x/sys/unix"
)

// HoldInput turns off the terminal's echo while output streams: echoed
// typing moves the cursor under the in-place redraws. What is typed stays
// queued for the next prompt, and Ctrl+C still interrupts. restore turns
// the echo back on.
func HoldInput() (restore func()) {
	fd := int(os.Stdin.Fd())
	t, err := unix.IoctlGetTermios(fd, getTermios)
	if err != nil || t.Lflag&unix.ECHO == 0 {
		return func() {}
	}
	old := *t
	t.Lflag &^= unix.ECHO
	if unix.IoctlSetTermios(fd, setTermios, t) != nil {
		return func() {}
	}
	return func() { unix.IoctlSetTermios(fd, setTermios, &old) }
}

// DiscardInput drops what was typed ahead, so it cannot answer a prompt
// the user has not seen yet.
func DiscardInput() { flushInput(int(os.Stdin.Fd())) }
