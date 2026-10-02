//go:build !windows

package ui

// consoleCursorRow is for Windows; elsewhere the terminal is asked.
func consoleCursorRow() int { return 0 }
