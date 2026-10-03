//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package ui

import "golang.org/x/sys/unix"

const (
	getTermios = unix.TIOCGETA
	setTermios = unix.TIOCSETA
)

func flushInput(fd int) { unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, 1) } // FREAD
