package tools

import (
	"fmt"
	"strings"
)

var defaultBlocklist = []string{
	"rm -rf /",
	"rm -rf /*",
	"mkfs",
	"dd if=",
	"> /dev/sd",
	"> /dev/nvme",
	"shutdown",
	"reboot",
	":(){ :|:& };:",
}

// IsSafe checks if a command is safe to execute against the blocklist.
// Returns (safe, reason).
func IsSafe(command string) (bool, string) {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, blocked := range defaultBlocklist {
		if strings.Contains(lower, blocked) {
			return false, fmt.Sprintf("blocked pattern: %q", blocked)
		}
	}
	return true, ""
}

const maxOutputBytes = 10 * 1024 // 10KB

// TruncateOutput truncates output to maxBytes, appending a truncation notice.
func TruncateOutput(output string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = maxOutputBytes
	}
	if len(output) <= maxBytes {
		return output
	}
	return output[:maxBytes] + "\n... [output truncated at 10KB]"
}
