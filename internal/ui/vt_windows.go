package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnableVT turns on escape-sequence processing for stdout and stderr where
// they are consoles. The classic console (cmd.exe or PowerShell in conhost)
// starts with it off: bubbletea turns it on only while a program runs and
// restores the mode it found, so the chat's transcript, printed after, came
// out as raw escape codes. With it on from the start, every program
// restores it on. It stays on after the CLI exits: shells set the modes
// they need, and turning it off could pull it from under another
// docsgpt-cli on the same console (`docsgpt-cli … | docsgpt-cli …`).
func EnableVT() {
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		h := windows.Handle(f.Fd())
		var mode uint32
		if windows.GetConsoleMode(h, &mode) == nil && mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
			windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
		}
	}
}
