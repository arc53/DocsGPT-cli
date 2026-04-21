package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"docsgpt-cli/internal/display"

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

func copyToClipboard(command string) {
	trimmedCommand := strings.TrimSpace(command)
	err := clipboard.WriteAll(trimmedCommand)
	if err != nil {
		printError("Failed to copy to clipboard: " + err.Error())
	} else {
		fmt.Printf("%s %s\n", display.Success("Command copied to clipboard:"), display.Success(trimmedCommand))
	}
}
