//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package display

import (
	"os"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// queryBackground asks the terminal for its background color (OSC 11),
// followed by a device-attributes request every terminal answers, so one
// without OSC 11 support replies at once instead of running out the clock.
// It returns the raw reply, or "" when the terminal stayed silent until the
// deadline.
func queryBackground(timeout time.Duration) string {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return ""
	}
	defer tty.Close()
	fd := int(tty.Fd())

	// A background job touching the terminal would be stopped (SIGTTOU/SIGTTIN).
	if pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP); err != nil || pgrp != unix.Getpgrp() {
		return ""
	}
	state, err := term.MakeRaw(tty.Fd())
	if err != nil {
		return ""
	}
	defer term.Restore(tty.Fd(), state)

	if _, err := tty.WriteString("\x1b]11;?\x07\x1b[c"); err != nil {
		return ""
	}
	var reply []byte
	deadline := time.Now().Add(timeout)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return ""
		}
		var fds unix.FdSet
		fds.Set(fd)
		tv := unix.NsecToTimeval(left.Nanoseconds())
		n, err := unix.Select(fd+1, &fds, nil, nil, &tv)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			return ""
		}
		buf := make([]byte, 256)
		n, err = tty.Read(buf)
		if err != nil {
			return ""
		}
		reply = append(reply, buf[:n]...)
		if daReply.Match(reply) {
			return string(reply)
		}
	}
}
