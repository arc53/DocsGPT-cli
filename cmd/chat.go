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

	prompt "github.com/c-bata/go-prompt"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session",
	Long: `Start an interactive multi-turn chat session with DocsGPT.

Special commands:
    /quit   - Exit the chat session
    /clear  - Clear conversation history
    /copy   - Copy the last code block to clipboard
    /think  - Toggle reasoning visibility

Type "/" to see available commands with live autocomplete.`,
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

		cwd, _ := os.Getwd()
		fmt.Println(display.RenderHeader(keyName, baseURL, cwd))
		if hints := display.RenderHints("chat"); hints != "" {
			fmt.Println(hints)
		}
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

// chatSession holds the mutable state for an interactive chat.
type chatSession struct {
	client        *api.Client
	history       []api.Message
	lastAnswer    string
	showReasoning bool
	toolDefs      []api.Tool
	timeout       time.Duration
}

func (s *chatSession) executor(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}

	switch input {
	case "/quit":
		fmt.Println("Goodbye!")
		os.Exit(0)
	case "/clear":
		var newHistory []api.Message
		if len(s.history) > 0 && s.history[0].Role == "system" {
			newHistory = append(newHistory, s.history[0])
		}
		s.history = newHistory
		s.lastAnswer = ""
		fmt.Println("History cleared.")
		return
	case "/copy":
		if s.lastAnswer == "" {
			printError("No previous response to copy from.")
			return
		}
		command := extractCommand(s.lastAnswer)
		if command != "" {
			copyToClipboard(command)
		} else {
			printError("No code block found in last response.")
		}
		return
	case "/think":
		s.showReasoning = !s.showReasoning
		if s.showReasoning {
			fmt.Println(display.Muted("Reasoning: visible"))
		} else {
			fmt.Println(display.Muted("Reasoning: hidden"))
		}
		return
	}

	s.history = append(s.history, api.Message{Role: "user", Content: input})
	ctx := context.Background()

	renderer := display.NewStreamRenderer()
	renderer.ShowReasoning = s.showReasoning

	onDelta := func(delta api.Delta, finishReason string) {
		renderer.Delta(delta)
	}

	onToolCall := func(tc api.ToolCall) string {
		return handleToolCall(tc, s.timeout)
	}

	updatedHistory, err := s.client.RunWithTools(
		ctx, s.history, s.toolDefs, !globalNoStream, onDelta, onToolCall,
	)
	if err != nil {
		printError(err.Error())
		return
	}
	fmt.Println()

	if rendered := renderer.Finish(); rendered != "" {
		fmt.Print(rendered)
	}

	s.history = updatedHistory
	s.lastAnswer = renderer.Content()

	fmt.Println()
}

func (s *chatSession) completer(d prompt.Document) []prompt.Suggest {
	text := d.TextBeforeCursor()
	if !strings.HasPrefix(text, "/") {
		return nil
	}

	suggestions := []prompt.Suggest{
		{Text: "/quit", Description: "Exit the chat session"},
		{Text: "/clear", Description: "Clear conversation history"},
		{Text: "/copy", Description: "Copy last code block to clipboard"},
		{Text: "/think", Description: "Toggle reasoning visibility"},
	}

	return prompt.FilterHasPrefix(suggestions, text, true)
}

func runChatLoop(client *api.Client, history []api.Message) error {
	var toolDefs []api.Tool
	if !globalNoContext {
		toolDefs = tools.ToolDefinitions()
	}

	session := &chatSession{
		client:   client,
		history:  history,
		toolDefs: toolDefs,
		timeout:  time.Duration(globalTimeout) * time.Second,
	}

	p := prompt.New(
		session.executor,
		session.completer,
		prompt.OptionPrefix("> "),
		prompt.OptionPrefixTextColor(prompt.Purple),
		prompt.OptionSuggestionBGColor(prompt.DarkGray),
		prompt.OptionSuggestionTextColor(prompt.White),
		prompt.OptionSelectedSuggestionBGColor(prompt.Purple),
		prompt.OptionSelectedSuggestionTextColor(prompt.White),
		prompt.OptionDescriptionBGColor(prompt.DarkGray),
		prompt.OptionDescriptionTextColor(prompt.White),
		prompt.OptionSelectedDescriptionBGColor(prompt.Purple),
		prompt.OptionSelectedDescriptionTextColor(prompt.White),
		prompt.OptionScrollbarBGColor(prompt.DarkGray),
		prompt.OptionScrollbarThumbColor(prompt.Purple),
		prompt.OptionShowCompletionAtStart(),
	)
	p.Run()
	return nil
}

func handleToolCall(tc api.ToolCall, timeout time.Duration) string {
	normalizedName := tools.NormalizeName(tc.Function.Name)

	// Check safety for run_command
	if normalizedName == "run_command" {
		safe, reason := tools.IsSafe(tc.Function.Arguments)
		if !safe {
			fmt.Printf("\n%s Command blocked: %s\n", display.Danger("✗"), reason)
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
