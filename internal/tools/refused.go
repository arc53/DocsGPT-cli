package tools

import "strings"

// Refused reports whether result is what Handle returned for a call that
// did not run: denied, interrupted at the prompt, or not approvable.
func Refused(result string) bool {
	for _, prefix := range []string{
		"The user denied this tool call.",
		"The user interrupted the run before this tool call ran.",
		"The tool call could not be approved: ",
	} {
		if strings.HasPrefix(result, prefix) {
			return true
		}
	}
	return false
}
