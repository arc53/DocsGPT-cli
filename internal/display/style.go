package display

import "fmt"

// Accent renders text in the accent color (prompts, headings).
func Accent(s string) string {
	return T.Accent.Render(s)
}

// Muted renders text in the muted/secondary color.
func Muted(s string) string {
	return T.Muted.Render(s)
}

// Success renders text in the success color.
func Success(s string) string {
	return T.Success.Render(s)
}

// Warn renders text in the warning color.
func Warn(s string) string {
	return T.Warn.Render(s)
}

// Danger renders text in the danger/error color.
func Danger(s string) string {
	return T.Danger.Render(s)
}

// Info renders text in the info/highlight color.
func Info(s string) string {
	return T.Info.Render(s)
}

// ErrorMsg prints a formatted error message.
func ErrorMsg(message string) {
	fmt.Printf("%s %s\n", Danger("Error:"), message)
}

// Prompt renders a prompt symbol in accent color.
func Prompt(symbol string) string {
	return T.Accent.Render(symbol)
}

// KeyValue renders a key in accent and value in default text.
func KeyValue(key, value string) string {
	return fmt.Sprintf("%s %s", Accent(key), value)
}
