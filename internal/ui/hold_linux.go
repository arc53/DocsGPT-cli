package ui

import "golang.org/x/sys/unix"

const (
	getTermios = unix.TCGETS
	setTermios = unix.TCSETS
)

func flushInput(fd int) { unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH) }
