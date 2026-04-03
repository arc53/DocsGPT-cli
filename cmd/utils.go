package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/fatih/color"
)

func printError(message string) {
	red := color.New(color.FgRed).SprintFunc()
	fmt.Printf("%s %s\n", red("Error:"), message)
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
		green := color.New(color.FgGreen).SprintFunc()
		fmt.Printf("%s %s\n", green("Command copied to clipboard:"), green(trimmedCommand))
	}
}
