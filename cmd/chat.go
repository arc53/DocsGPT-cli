package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"docsgpt-cli/internal/api"
	"docsgpt-cli/internal/config"
	ctxenrich "docsgpt-cli/internal/context"
	"docsgpt-cli/internal/display"
	"docsgpt-cli/internal/tools"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session",
	Long: `Start an interactive multi-turn chat session with DocsGPT.

Special commands:
    /quit   - Exit the chat session
    /clear  - Clear conversation history
    /copy   - Copy the last code block to clipboard`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		keyName, apiKey, err := cfg.ResolveKey(globalKey)
		if err != nil {
			return err
		}

		baseURL := cfg.ResolveURL(globalURL)
		client := api.NewClient(baseURL, apiKey)

		green := color.New(color.FgGreen).SprintFunc()
		fmt.Printf(green("Connected with key: %s\n"), keyName)
		fmt.Printf(green("Server: %s\n"), baseURL)
		fmt.Println("Type /quit to exit, /clear to reset history, /copy to copy last code block.")
		fmt.Println()

		var history []api.Message

		// Optionally add context as system message
		if !globalNoContext {
			ctx := ctxenrich.BuildContext(cfg.Settings)
			if ctx != "" {
				history = append(history, api.Message{
					Role:    "system",
					Content: "Here is context about the user's environment:\n" + ctx,
				})
			}
		}

		return runChatLoop(client, history)
	},
}

func runChatLoop(client *api.Client, history []api.Message) error {
	reader := bufio.NewReader(os.Stdin)
	var lastAnswer string

	// Handle Ctrl+C gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\nGoodbye!")
		os.Exit(0)
	}()

	toolDefs := tools.ToolDefinitions()
	timeout := time.Duration(globalTimeout) * time.Second

	for {
		green := color.New(color.FgGreen).SprintFunc()
		fmt.Print(green("> "))

		input, err := reader.ReadString('\n')
		if err != nil {
			return nil // EOF
		}
		input = strings.TrimSpace(input)

		if input == "" {
			continue
		}

		switch input {
		case "/quit":
			fmt.Println("Goodbye!")
			return nil
		case "/clear":
			// Keep system message if present
			var newHistory []api.Message
			if len(history) > 0 && history[0].Role == "system" {
				newHistory = append(newHistory, history[0])
			}
			history = newHistory
			lastAnswer = ""
			fmt.Println("History cleared.")
			continue
		case "/copy":
			if lastAnswer == "" {
				printError("No previous response to copy from.")
				continue
			}
			command := extractCommand(lastAnswer)
			if command != "" {
				copyToClipboard(command)
			} else {
				printError("No code block found in last response.")
			}
			continue
		}

		history = append(history, api.Message{Role: "user", Content: input})
		ctx := context.Background()

		onDelta := func(delta api.Delta, finishReason string) {
			display.StreamDelta(delta)
		}

		onToolCall := func(tc api.ToolCall) string {
			return handleToolCall(tc, timeout)
		}

		updatedHistory, err := client.RunWithTools(
			ctx, history, toolDefs, !globalNoStream, onDelta, onToolCall,
		)
		if err != nil {
			printError(err.Error())
			continue
		}
		fmt.Println()

		history = updatedHistory

		// Find the last assistant message for lastAnswer
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "assistant" {
				lastAnswer = history[i].Content
				break
			}
		}

		fmt.Println()
	}
}

func handleToolCall(tc api.ToolCall, timeout time.Duration) string {
	normalizedName := tools.NormalizeName(tc.Function.Name)

	// Check safety for run_command
	if normalizedName == "run_command" {
		safe, reason := tools.IsSafe(tc.Function.Arguments)
		if !safe {
			red := color.New(color.FgRed).SprintFunc()
			fmt.Printf("\n%s Command blocked: %s\n", red("✗"), reason)
			return fmt.Sprintf("Command was blocked for safety: %s", reason)
		}
	}

	// Auto-approve or ask user
	args := tc.Function.Arguments
	if !globalAutoApprove {
		result, editedArgs, err := tools.RequestApproval(normalizedName, args)
		if err != nil {
			return "Error during approval: " + err.Error()
		}
		switch result {
		case tools.Denied:
			return "User denied this tool call."
		case tools.Edited:
			args = editedArgs
		}
	}

	// Execute
	toolResult := tools.Execute(tc.Function.Name, args, timeout)
	return toolResult.String()
}
