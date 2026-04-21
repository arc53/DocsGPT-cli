package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"docsgpt-cli/internal/api"
	"docsgpt-cli/internal/config"
	ctxenrich "docsgpt-cli/internal/context"
	"docsgpt-cli/internal/display"
	"docsgpt-cli/internal/tools"

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

		cwd, _ := os.Getwd()
		fmt.Println(display.RenderHeader(keyName, baseURL, cwd))
		fmt.Print(display.Prompt("❯ "))

		ctx := context.Background()
		var toolDefs []api.Tool
		if !globalNoContext {
			toolDefs = tools.ToolDefinitions()
		}
		timeout := time.Duration(globalTimeout) * time.Second

		renderer := display.NewStreamRenderer()

		onDelta := func(delta api.Delta, finishReason string) {
			renderer.Delta(delta)
		}

		onToolCall := func(tc api.ToolCall) string {
			return handleToolCall(tc, timeout)
		}

		_, err = client.RunWithTools(
			ctx, messages, toolDefs, !globalNoStream, onDelta, onToolCall,
		)
		if err != nil {
			return err
		}
		fmt.Println()

		// Render markdown for the final answer if it contains formatting
		if rendered := renderer.Finish(); rendered != "" {
			fmt.Print(rendered)
		}

		answer := renderer.Content()
		command := extractCommand(answer)
		if command != "" {
			copyToClipboard(command)
		}

		return nil
	},
}
