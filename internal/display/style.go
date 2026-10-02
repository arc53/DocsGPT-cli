package display

import (
	"fmt"
	"os"
)

// Accent renders text in the accent color (prompts, headings).
func Accent(s string) string {
	return T.Accent.Render(s)
}

// Muted renders text in the muted/secondary color.
func Muted(s string) string {
	return T.Muted.Render(s)
}

// Dim renders text in the dim color (chrome, hints, metadata).
func Dim(s string) string {
	return T.Dim.Render(s)
}

// Success renders text in the success color.
func Success(s string) string {
	return T.Success.Render(s)
}

// Warn renders text in the warning color.
func Warn(s string) string {
	return T.Warning.Render(s)
}

// Danger renders text in the error color.
func Danger(s string) string {
	return T.Error.Render(s)
}

// Info renders text in the link color.
func Info(s string) string {
	return T.Link.Render(s)
}

// ErrorMsg prints "✗ message" in the error color to stderr.
func ErrorMsg(message string) {
	fmt.Fprintln(os.Stderr, T.Error.Render("✗ "+message))
}
