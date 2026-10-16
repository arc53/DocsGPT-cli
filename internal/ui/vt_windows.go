package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnableVT turns on escape-sequence processing for stdout and stderr where
// they are consoles, and returns what puts the consoles' modes back. The
// classic console (cmd.exe or PowerShell in conhost) starts with it off:
// bubbletea turns it on only while a program runs and restores the mode it
// found, so the chat's transcript, printed after, came out as raw escape
// codes. With it on from the start, every program restores it on.
func EnableVT() (restore func()) {
	var undo []func()
	seen := map[windows.Handle]bool{}
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		h := windows.Handle(f.Fd())
		var mode uint32
		if seen[h] || windows.GetConsoleMode(h, &mode) != nil {
			continue
		}
		seen[h] = true
		if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
			continue
		}
		if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil {
			undo = append(undo, func() { windows.SetConsoleMode(h, mode) })
		}
	}
	return func() {
		for _, u := range undo {
			u()
		}
	}
}
