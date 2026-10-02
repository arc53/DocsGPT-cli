package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// consoleCursorRow returns the window row (from 1) of the console's cursor,
// or 0 when it cannot tell.
func consoleCursorRow() int {
	var info windows.ConsoleScreenBufferInfo
	if windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info) != nil {
		return 0
	}
	return max(0, int(info.CursorPosition.Y-info.Window.Top)+1)
}
