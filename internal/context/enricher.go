package context

import (
	"fmt"
	"os"
	"os/user"
	"strings"

	"docsgpt-cli/internal/config"
)

// BuildContext creates a context string based on the user's settings.
func BuildContext(settings config.Settings) string {
	var context string

	if settings.SendCurrentDirectory {
		currentPath, _ := os.Getwd()
		context += fmt.Sprintf("CURRENT_DIRECTORY: %s\n", currentPath)
	}

	if settings.SendDirectoryContents {
		currentPath, _ := os.Getwd()
		files, _ := os.ReadDir(currentPath)

		var fileNames []string
		for _, file := range files {
			fileNames = append(fileNames, file.Name())
		}
		directoryContents := strings.Join(fileNames, ", ")
		context += fmt.Sprintf("DIRECTORY_CONTENTS: %s\n", directoryContents)
	}

	if settings.SendLastCommands {
		lastCommands := GetLastCommands(settings.NumberOfLastCommands)
		context += fmt.Sprintf("LAST_COMMANDS:\n%s\n", lastCommands)
	}

	return context
}

// BuildQuestion formats a question with optional context.
func BuildQuestion(question string, settings config.Settings, includeContext bool) string {
	if !includeContext {
		return question
	}
	ctx := BuildContext(settings)
	if ctx == "" {
		return question
	}
	return fmt.Sprintf("QUESTION: %s\n\n%s", question, ctx)
}

// GetLastCommands reads the last n commands from the user's shell history.
func GetLastCommands(n int) string {
	shell := os.Getenv("SHELL")
	var historyFile string

	usr, _ := user.Current()
	homeDir := usr.HomeDir

	if strings.Contains(shell, "zsh") {
		historyFile = fmt.Sprintf("%s/.zsh_history", homeDir)
	} else if strings.Contains(shell, "bash") {
		historyFile = fmt.Sprintf("%s/.bash_history", homeDir)
	} else if strings.Contains(shell, "fish") {
		historyFile = fmt.Sprintf("%s/.local/share/fish/fish_history", homeDir)
	} else {
		return "Unknown shell"
	}

	data, err := os.ReadFile(historyFile)
	if err != nil {
		return "Could not read history"
	}

	lines := strings.Split(string(data), "\n")
	var commands []string

	if strings.Contains(shell, "zsh") || strings.Contains(shell, "bash") {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, ":") {
				parts := strings.SplitN(line, ";", 2)
				if len(parts) == 2 {
					commands = append(commands, parts[1])
				}
			} else if line != "" {
				commands = append(commands, line)
			}
		}
	} else if strings.Contains(shell, "fish") {
		for _, line := range lines {
			if strings.HasPrefix(line, "- cmd: ") {
				commands = append(commands, strings.TrimPrefix(line, "- cmd: "))
			}
		}
	} else {
		return "Unsupported shell"
	}

	if len(commands) > n {
		return strings.Join(commands[len(commands)-n:], "\n")
	}
	return strings.Join(commands, "\n")
}
