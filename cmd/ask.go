package cmd

import (
	"context"
	"fmt"
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

var askCmd = &cobra.Command{
	Use:   "ask",
	Short: "Ask a question to DocsGPT",
	Long: `Ask a question to DocsGPT, and instantly find answers about anything.

Example usage:
    docsgpt-cli ask "How do I open a file in Python?"

This command will provide a contextual answer and, if applicable, copy a relevant code snippet to your clipboard.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("please provide a question")
		}

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

		question := strings.Join(args, " ")
		includeContext := !globalNoContext
		fullQuestion := ctxenrich.BuildQuestion(question, cfg.Settings, includeContext)

		messages := []api.Message{
			{Role: "user", Content: fullQuestion},
		}

		green := color.New(color.FgGreen).SprintFunc()
		fmt.Printf(green("Key: %s\n"), keyName)
		fmt.Printf(green(" ❯ "))

		ctx := context.Background()
		toolDefs := tools.ToolDefinitions()
		timeout := time.Duration(globalTimeout) * time.Second

		onDelta := func(delta api.Delta, finishReason string) {
			display.StreamDelta(delta)
		}

		onToolCall := func(tc api.ToolCall) string {
			return handleToolCall(tc, timeout)
		}

		updatedHistory, err := client.RunWithTools(
			ctx, messages, toolDefs, !globalNoStream, onDelta, onToolCall,
		)
		if err != nil {
			return err
		}
		fmt.Println()

		// Find the last assistant message for clipboard
		var answer string
		for i := len(updatedHistory) - 1; i >= 0; i-- {
			if updatedHistory[i].Role == "assistant" {
				answer = updatedHistory[i].Content
				break
			}
		}

		command := extractCommand(answer)
		if command != "" {
			copyToClipboard(command)
		}

		return nil
	},
}
