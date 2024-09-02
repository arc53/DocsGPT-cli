package cmd

import (
    "bytes"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "os/user"
    "regexp"
    "strings"
    "time"

    "github.com/atotto/clipboard"
    "github.com/spf13/cobra"
    "github.com/fatih/color"
)

var askCmd = &cobra.Command{
    Use:   "ask",
    Short: "Ask a question to DocsGPT",
    Long: `Ask a question to DocsGPT, and instantly find answers about anything.

Example usage:
    docsgpt-cli ask "How do I open a file in Python?"

This command will provide a contextual answer and, if applicable, copy a relevant code snippet to your clipboard.`,
    Run: func(cmd *cobra.Command, args []string) {
        keys := loadKeys()
        keyName, key := getDefaultKey(keys)
        if key.Key == "" {
            return
        }

        questionWithContext := getQuestionWithContext(args)

        done := make(chan bool)
        go spinner(done) // Start the spinner in a separate goroutine

        green := color.New(color.FgGreen).SprintFunc()
        fmt.Printf(green("Key: %s\n"), keyName) // Print the name of the selected key

        answer, err := requestDocsgpt(questionWithContext, key.Key)
        done <- true // Stop the spinner
        if err != nil {
            printError(err.Error())
            return
        }

        fmt.Printf(green(" ❯ "))
        fmt.Println(answer)

        command := extractCommand(answer)
        if command != "" {
            copyToClipboard(command)
        }
    },
}

func getQuestionWithContext(args []string) string {
    settings := loadSettings()
    question := strings.Join(args, " ")

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
        lastCommands := getLastCommands(settings.NumberOfLastCommands)
        context += fmt.Sprintf("LAST_COMMANDS:\n%s\n", lastCommands)
    }

    return fmt.Sprintf("QUESTION: %s\n\n%s", question, context)
}

func getLastCommands(n int) string {
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

    // Process zsh and bash history to strip out timestamps and other metadata
    if strings.Contains(shell, "zsh") || strings.Contains(shell, "bash") {
        for _, line := range lines {
            line = strings.TrimSpace(line) // Remove leading/trailing whitespace
            if strings.HasPrefix(line, ":") {
                // For zsh history lines that look like `: 1623390275:0;command`
                parts := strings.SplitN(line, ";", 2)
                if len(parts) == 2 {
                    commands = append(commands, parts[1])
                }
            } else if line != "" {
                // Include lines without timestamps (typical in .bash_history)
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

    // Ensure we're only taking the last n commands
    if len(commands) > n {
        return strings.Join(commands[len(commands)-n:], "\n")
    }
    return strings.Join(commands, "\n")
}

func requestDocsgpt(question, apiKey string) (string, error) {
    payload := map[string]string{"question": question, "api_key": apiKey}
    jsonPayload, _ := json.Marshal(payload)

    resp, err := http.Post("https://gptcloud.arc53.com/api/answer", "application/json", bytes.NewBuffer(jsonPayload))
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    var result map[string]interface{}
    _ = json.NewDecoder(resp.Body).Decode(&result)

    if answer, found := result["answer"].(string); found {
        return answer, nil
    }
    return "", fmt.Errorf("no answer in response")
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

func spinner(done chan bool) {
    for {
        select {
        case <-done:
            fmt.Print("\r") // Clear the spinner
            return
        default:
            for _, r := range `-\|/` {
                fmt.Printf("\r[%c] Loading...", r)
                time.Sleep(100 * time.Millisecond)
            }
        }
    }
}
