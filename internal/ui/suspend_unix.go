//go:build !windows

package ui

import (
	"os"
	"os/signal"
	"syscall"
)

// suspendProcess stops the process as Ctrl+Z does in a shell, returning
// once it is continued (fg).
var suspendProcess = func() {
	cont := make(chan os.Signal, 1)
	signal.Notify(cont, syscall.SIGCONT)
	defer signal.Stop(cont)
	if syscall.Kill(0, syscall.SIGTSTP) == nil {
		<-cont
	}
}
