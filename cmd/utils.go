package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/display"

	"github.com/atotto/clipboard"
)

func printError(message string) {
	display.ErrorMsg(message)
}

func extractCommand(answer string) string {
	re := regexp.MustCompile("(?s)```bash(.*?)```")
	match := re.FindStringSubmatch(answer)
	if len(match) > 1 {
		return match[1]
	}

	re = regexp.MustCompile("(?s)```sh(.*?)```")
	match = re.FindStringSubmatch(answer)
	if len(match) > 1 {
		return match[1]
	}

	return ""
}

// copyToClipboard copies command and says so in a dim line.
func copyToClipboard(command string) {
	command = strings.TrimSpace(command)
	if err := clipboard.WriteAll(command); err != nil {
		printError("Failed to copy to clipboard: " + err.Error())
		return
	}
	first, rest, multi := strings.Cut(command, "\n")
	if multi {
		first += fmt.Sprintf(" … (+%d lines)", strings.Count(rest, "\n")+1)
	}
	fmt.Println(display.Muted("Copied to clipboard: " + first))
}
