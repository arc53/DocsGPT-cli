package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	ctxenrich "github.com/arc53/DocsGPT-cli/internal/context"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

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
		client := docsgpt.NewClient(baseURL, apiKey)

		display.ShowBanner(cfg.Settings.Banner)
		cwd, _ := os.Getwd()
		fmt.Println(display.RenderHeader(Version, keyName, baseURL, cwd))
		fmt.Println(display.RenderHints())
		fmt.Println()

		var history []docsgpt.Message

		// Optionally add context as system message
		if !globalNoContext {
			ctx := ctxenrich.BuildContext(cfg.Settings)
			if ctx != "" {
				history = append(history, docsgpt.Message{
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
	client        *docsgpt.Client
	history       []docsgpt.Message
	lastAnswer    string
	showReasoning bool
	toolDefs      []docsgpt.Tool
	tools         *tools.Session
	// conversationID continues the server-side conversation, which then
	// supplies the history (the messages are still sent, for servers that
	// ignore the id).
	conversationID string
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
		var newHistory []docsgpt.Message
		if len(s.history) > 0 && s.history[0].Role == "system" {
			newHistory = append(newHistory, s.history[0])
		}
		s.history = newHistory
		s.lastAnswer = ""
		s.conversationID = ""
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

	s.history = append(s.history, docsgpt.Message{Role: "user", Content: input})

	// The prompt library restores cooked mode (ISIG on) while the executor
	// runs, so Ctrl-C here is a real SIGINT. Turn it into a cancellation of
	// the in-flight request instead of letting it kill the whole session.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	renderer := display.NewStreamRenderer()
	renderer.ShowReasoning = s.showReasoning

	onDelta := func(delta docsgpt.Delta, finishReason string) {
		renderer.Delta(delta)
	}

	onToolCall := func(tc docsgpt.ToolCall) string {
		renderer.Flush()
		defer renderer.Wait()
		return s.tools.Handle(ctx, cancel, tc)
	}

	renderer.Wait()
	res, err := s.client.RunWithTools(ctx, s.history, docsgpt.RunOptions{
		Tools: s.toolDefs, Stream: !globalNoStream, ConversationID: s.conversationID,
		OnDelta: onDelta, OnToolCall: onToolCall,
	})
	renderer.Flush()
	if err != nil {
		// Drop the user turn that never got an answer so the next
		// message doesn't carry a dangling question.
		s.history = s.history[:len(s.history)-1]
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			fmt.Println(display.Muted("Interrupted."))
			return
		}
		// The server may have refused the conversation (deleted, say):
		// the next turn starts a new one from the messages.
		s.conversationID = ""
		printError(err.Error())
		return
	}

	if line := sourcesLine(res.Sources); line != "" {
		fmt.Println(display.Muted(line))
	}
	s.history = res.Messages
	s.conversationID = res.ConversationID
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

func runChatLoop(client *docsgpt.Client, history []docsgpt.Message) error {
	var toolDefs []docsgpt.Tool
	if !globalNoContext {
		toolDefs = tools.ToolDefinitions()
	}

	session := &chatSession{
		client:   client,
		history:  history,
		toolDefs: toolDefs,
		tools:    &tools.Session{AutoApprove: globalAutoApprove, Timeout: time.Duration(globalTimeout) * time.Second},
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
