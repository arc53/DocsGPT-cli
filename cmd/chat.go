package cmd

import (
	"context"
	"errors"
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

	prompt "github.com/elk-language/go-prompt"
	pstrings "github.com/elk-language/go-prompt/strings"
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

Keys: Ctrl+C interrupts a streaming answer (or clears the input line),
Ctrl+D on an empty line exits. Type "/" to see available commands with
live autocomplete.`,
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

	// The prompt library restores cooked mode (ISIG on) while the executor
	// runs, so Ctrl-C here is a real SIGINT. Turn it into a cancellation of
	// the in-flight request instead of letting it kill the whole session.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	renderer := display.NewStreamRenderer()
	renderer.ShowReasoning = s.showReasoning

	onDelta := func(delta api.Delta, finishReason string) {
		renderer.Delta(delta)
	}

	onToolCall := func(tc api.ToolCall) string {
		return handleToolCall(ctx, tc, s.timeout)
	}

	updatedHistory, err := s.client.RunWithTools(
		ctx, s.history, s.toolDefs, !globalNoStream, onDelta, onToolCall,
	)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			// Drop the user turn that never got an answer so the next
			// message doesn't carry a dangling question.
			s.history = s.history[:len(s.history)-1]
			fmt.Println()
			fmt.Println(display.Muted("Interrupted."))
			return
		}
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

// completer offers the slash commands while the line starts with "/". It
// returns the rune range the chosen suggestion replaces (the whole text
// before the cursor), as the prompt library requires.
func (s *chatSession) completer(d prompt.Document) ([]prompt.Suggest, pstrings.RuneNumber, pstrings.RuneNumber) {
	text := d.TextBeforeCursor()
	end := d.CurrentRuneIndex()
	if !strings.HasPrefix(text, "/") {
		return nil, end, end
	}

	suggestions := []prompt.Suggest{
		{Text: "/quit", Description: "Exit the chat session"},
		{Text: "/clear", Description: "Clear conversation history"},
		{Text: "/copy", Description: "Copy last code block to clipboard"},
		{Text: "/think", Description: "Toggle reasoning visibility"},
	}

	start := end - pstrings.RuneCountInString(text)
	return prompt.FilterHasPrefix(suggestions, text, true), start, end
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
		prompt.WithCompleter(session.completer),
		prompt.WithPrefix("> "),
		prompt.WithPrefixTextColor(prompt.Purple),
		prompt.WithSuggestionBGColor(prompt.DarkGray),
		prompt.WithSuggestionTextColor(prompt.White),
		prompt.WithSelectedSuggestionBGColor(prompt.Purple),
		prompt.WithSelectedSuggestionTextColor(prompt.White),
		prompt.WithDescriptionBGColor(prompt.DarkGray),
		prompt.WithDescriptionTextColor(prompt.White),
		prompt.WithSelectedDescriptionBGColor(prompt.Purple),
		prompt.WithSelectedDescriptionTextColor(prompt.White),
		prompt.WithScrollbarBGColor(prompt.DarkGray),
		prompt.WithScrollbarThumbColor(prompt.Purple),
		prompt.WithShowCompletionAtStart(),
	)
	p.Run()
	return nil
}

// handleToolCall gates a model-requested tool call behind the safety check
// and the user's approval, then executes it. A cancelled ctx (Ctrl-C) skips
// the call: before the approval prompt, and again after it, so a Ctrl-C
// pressed while the prompt was waiting never runs the command.
func handleToolCall(ctx context.Context, tc api.ToolCall, timeout time.Duration) string {
	if ctx.Err() != nil {
		return "User interrupted before this tool call ran."
	}
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
		if ctx.Err() != nil {
			return "User interrupted before this tool call ran."
		}
	}

	// Execute
	toolResult := tools.Execute(tc.Function.Name, args, timeout)
	return toolResult.String()
}
