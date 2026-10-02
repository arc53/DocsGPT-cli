package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	ctxenrich "github.com/arc53/DocsGPT-cli/internal/context"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:    "chat [message]",
	Short:  "Start an interactive chat session",
	Hidden: true,
	Long: `Start an interactive multi-turn chat session with DocsGPT.

Type / for the commands, !cmd to run a shell command and send its output with
your next message (!!cmd to keep it to yourself). Enter sends, Ctrl+J or
Alt+Enter (or a trailing \) starts a new line, ↑/↓ browse earlier prompts and
Ctrl+G edits the message in $EDITOR. Ctrl+C stops an answer or clears the
input; twice on an empty input (or Ctrl+D) quits.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runChat(strings.Join(args, " "))
	},
}

// chatSession holds the state of an interactive chat.
type chatSession struct {
	cfg     config.Config
	keyName string
	baseURL string
	client  *docsgpt.Client

	history []docsgpt.Message
	// conversationID continues the server-side conversation, which then
	// supplies the history (the messages are still sent, for servers that
	// ignore the id).
	conversationID string
	// sentContext is the context block the conversation last carried; it
	// is sent again only when it changes.
	sentContext string
	// shellOutput holds the output of !commands for the next message.
	shellOutput []string
	turns       []chatTurn // for /export
	lastAnswer  string

	showReasoning bool
	toolDefs      []docsgpt.Tool
	tools         *tools.Session
	quit          bool
}

// chatTurn is one question and its answer.
type chatTurn struct {
	question, answer string
	sources          []docsgpt.Source
}

// chatCommand is a slash command of the chat.
type chatCommand struct {
	name, args, desc string
	aliases          []string
	run              func(s *chatSession, arg string)
}

// chatCommands is the one list behind the popup, /help and dispatch (set
// in init: /help refers to it).
var chatCommands []chatCommand

func init() {
	chatCommands = []chatCommand{
		{name: "new", desc: "Start a new conversation", aliases: []string{"clear"}, run: (*chatSession).newConversation},
		{name: "copy", desc: "Copy the last answer, or one of its code blocks", run: (*chatSession).copyAnswer},
		{name: "export", args: "[file]", desc: "Save the conversation as markdown", run: (*chatSession).export},
		{name: "think", desc: "Show or hide the model's reasoning", run: (*chatSession).toggleThinking},
		{name: "key", desc: "Switch to another agent API key", run: (*chatSession).switchKey},
		{name: "settings", desc: "Change settings", run: (*chatSession).settings},
		{name: "help", desc: "Show commands and keys", run: (*chatSession).help},
		{name: "quit", desc: "Leave the chat", aliases: []string{"exit"}, run: func(s *chatSession, _ string) { s.quit = true }},
	}
}

func runChat(first string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	keyName, apiKey, err := chatKey(&cfg)
	if err != nil {
		return err
	}
	baseURL := cfg.ResolveURL(globalURL)
	s := &chatSession{
		cfg: cfg, keyName: keyName, baseURL: baseURL,
		client: docsgpt.NewClient(baseURL, apiKey),
		tools:  &tools.Session{AutoApprove: globalAutoApprove, Timeout: time.Duration(globalTimeout) * time.Second},
	}
	if !globalNoTools {
		s.toolDefs = tools.ToolDefinitions()
	}

	display.ShowBanner(cfg.Settings.Banner)
	fmt.Println(display.ChatWelcome(Version))
	fmt.Println()

	if first = strings.TrimSpace(first); first != "" {
		s.send(first, first)
	}
	editor := &ui.Editor{History: ui.LoadHistory(filepath.Join(config.Dir(), "history"))}
	for _, c := range chatCommands {
		editor.Commands = append(editor.Commands, ui.Command{Name: c.name, Description: c.desc})
	}
	for !s.quit {
		editor.Footer, editor.Status = s.footer()
		text, shown, err := editor.Run()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		s.handle(text, shown)
	}
	return nil
}

// footer returns the text under the input: where, as whom and what is on.
func (s *chatSession) footer() (left, right string) {
	cwd, _ := os.Getwd()
	var status []string
	if n := len(s.shellOutput); n > 0 {
		status = append(status, fmt.Sprintf("+%d command %s", n, plural(n, "output", "outputs")))
	}
	if s.showReasoning {
		status = append(status, "think on")
	}
	return display.ChatFooter(cwd, s.keyName, s.baseURL), strings.Join(status, " · ")
}

// handle acts on one submitted input: a command, a shell command or a
// message for the agent.
func (s *chatSession) handle(text, shown string) {
	line := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(line, "!"):
		s.shell(line)
	case strings.HasPrefix(line, "/") && s.command(line):
	default:
		s.send(text, shown)
	}
}

// command runs a slash command and reports whether line was one; a path
// such as "/etc/hosts …" is a message.
func (s *chatSession) command(line string) bool {
	name, arg, _ := strings.Cut(line[1:], " ")
	for _, c := range chatCommands {
		if c.name == name || slices.Contains(c.aliases, name) {
			c.run(s, strings.TrimSpace(arg))
			return true
		}
	}
	if strings.Contains(name, "/") {
		return false
	}
	printError("Unknown command /" + name + " (type / to see them)")
	fmt.Println()
	return true
}

// shell runs a command the user typed: "!cmd" sends its output along with
// the next message, "!!cmd" keeps it out.
func (s *chatSession) shell(line string) {
	keep := !strings.HasPrefix(line, "!!")
	command := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "!"), "!"))
	if command == "" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// The tool block opens with a blank line, which the input already left.
	fmt.Fprint(os.Stderr, "\x1b[1A")
	restore := ui.HoldInput()
	out := tools.RunShell(ctx, command)
	restore()
	if keep {
		s.shellOutput = append(s.shellOutput, fmt.Sprintf("I ran `%s`:\n```\n%s\n```", command, strings.TrimRight(out, "\n")))
	}
	fmt.Println()
}

// send asks the agent, showing the message, the answer and its sources.
func (s *chatSession) send(text, shown string) {
	display.UserMessage(shown)

	content := text
	if len(s.shellOutput) > 0 {
		content = strings.Join(s.shellOutput, "\n\n") + "\n\n" + content
	}
	block := ""
	if !globalNoContext {
		if block = ctxenrich.Build(s.cfg.Settings); block != s.sentContext {
			content = ctxenrich.Prepend(block, content)
		}
	}
	messages := append(slices.Clip(s.history), docsgpt.Message{Role: "user", Content: content})

	// The editor is not running, so the terminal is in cooked mode and
	// Ctrl+C is a real SIGINT: it cancels the request, not the chat.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	renderer := display.NewStreamRenderer()
	renderer.ShowReasoning = s.showReasoning
	renderer.Wait()
	restore := ui.HoldInput() // what is typed meanwhile waits for the editor
	defer restore()
	res, err := s.client.RunWithTools(ctx, messages, docsgpt.RunOptions{
		Tools: s.toolDefs, Stream: !globalNoStream, ConversationID: s.conversationID,
		OnDelta: func(delta docsgpt.Delta, _ string) { renderer.Delta(delta) },
		OnToolCall: func(tc docsgpt.ToolCall) string {
			renderer.Flush()
			defer renderer.Wait()
			ui.DiscardInput() // typed ahead, it would answer the approval
			return s.tools.Handle(ctx, cancel, tc)
		},
	})
	renderer.Flush()
	restore()
	if err != nil {
		if ctx.Err() != nil {
			gap := "" // after a partial answer
			if renderer.Content() != "" {
				gap = "\n"
			}
			fmt.Println("\r\x1b[2K" + gap + display.Dim("Interrupted.") + "\n")
			return
		}
		// The server may have refused the conversation (deleted, say):
		// the next turn starts a new one from the messages.
		s.conversationID = ""
		printError(err.Error())
		fmt.Println()
		return
	}
	display.PrintSources(res.Sources)
	fmt.Println()

	s.history, s.conversationID = res.Messages, res.ConversationID
	s.sentContext, s.shellOutput = block, nil
	s.lastAnswer = renderer.Content()
	s.turns = append(s.turns, chatTurn{text, s.lastAnswer, res.Sources})
}

// reset starts a new conversation.
func (s *chatSession) reset() {
	s.history, s.conversationID, s.sentContext = nil, "", ""
	s.shellOutput, s.turns, s.lastAnswer = nil, nil, ""
}

func (s *chatSession) newConversation(string) {
	s.reset()
	fmt.Println(display.Dim("New conversation.") + "\n")
}

func (s *chatSession) toggleThinking(string) {
	s.showReasoning = !s.showReasoning
	state := "hidden"
	if s.showReasoning {
		state = "shown"
	}
	fmt.Println(display.Dim("Reasoning will be "+state+".") + "\n")
}

// copyAnswer copies the last answer, or one of its code blocks.
func (s *chatSession) copyAnswer(string) {
	if s.lastAnswer == "" {
		printError("Nothing to copy yet.")
		fmt.Println()
		return
	}
	text := s.lastAnswer
	if blocks := codeBlocks(text); len(blocks) > 0 {
		items := []ui.Item{{Label: "Whole answer", Value: "-1", Description: lineCount(text)}}
		for i, b := range blocks {
			first, _, _ := strings.Cut(strings.TrimSpace(b.code), "\n")
			desc := lineCount(b.code)
			if b.lang != "" {
				desc = b.lang + " · " + desc
			}
			items = append(items, ui.Item{Label: first, Value: strconv.Itoa(i), Description: desc})
		}
		v, err := ui.Select{Title: "Copy", Items: items, Summary: func(ui.Item) string { return "" }}.Run()
		if err != nil {
			return
		}
		if i, _ := strconv.Atoi(v); i >= 0 {
			text = blocks[i].code
		}
	}
	copyToClipboard(text)
	fmt.Println()
}

func lineCount(s string) string {
	n := strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
	return fmt.Sprintf("%d %s", n, plural(n, "line", "lines"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// export writes the conversation as markdown, to file or
// docsgpt-<date>.md in the working directory.
func (s *chatSession) export(file string) {
	if len(s.turns) == 0 {
		printError("Nothing to export yet.")
		fmt.Println()
		return
	}
	if file == "" {
		file = "docsgpt-" + time.Now().Format("2006-01-02-150405") + ".md"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# DocsGPT chat · %s\n", time.Now().Format("2006-01-02 15:04"))
	for _, t := range s.turns {
		b.WriteString("\n## You\n\n" + strings.TrimSpace(t.question) + "\n\n## " + s.keyName + "\n\n" + strings.TrimSpace(t.answer) + "\n")
		for i, src := range t.sources {
			if i == 0 {
				b.WriteString("\nSources:\n\n")
			}
			title := src.Title
			if title == "" {
				title = src.Filename
			}
			if strings.HasPrefix(src.Source, "http") {
				title = "[" + title + "](" + src.Source + ")"
			}
			b.WriteString("- " + title + "\n")
		}
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
		printError(err.Error())
		fmt.Println()
		return
	}
	fmt.Println(display.Success("✓") + " " + display.Dim("Exported to "+file) + "\n")
}

// switchKey picks another stored key (or adds one) and starts a new
// conversation with it.
func (s *chatSession) switchKey(string) {
	if cfg, err := config.Load(); err == nil {
		s.cfg.Keys, s.cfg.DefaultKey = cfg.Keys, cfg.DefaultKey
	}
	host := hostOf(s.baseURL)
	var items []ui.Item
	current := 0
	for _, name := range sortedNames(s.cfg.Keys) {
		if name == s.keyName {
			current = len(items)
		}
		items = append(items, ui.Item{Label: name, Value: name, Description: config.RedactKey(s.cfg.Keys[name]) + " · " + host})
	}
	items = append(items, ui.Item{Label: "Add a key…", Value: "\x00add"})
	name, err := ui.Select{Title: "Chat with", Items: items, Default: current, Filter: len(items) > 8, Summary: func(ui.Item) string { return "" }}.Run()
	if err != nil {
		return
	}
	if name == "\x00add" {
		cfg := s.cfg
		if name, err = addCredential(context.Background(), &cfg, true, os.Stdout); err != nil {
			if !errors.Is(err, ui.ErrCancelled) {
				printError(err.Error())
			}
			fmt.Println()
			return
		}
		s.cfg.Keys = cfg.Keys
	}
	if name == s.keyName {
		return
	}
	s.keyName, s.client.APIKey = name, s.cfg.Keys[name]
	s.reset()
	fmt.Println(display.Success("✓") + " " + display.Dim("Chatting with "+name+", in a new conversation.") + "\n")
}

func (s *chatSession) settings(string) {
	saved, err := runConfigMenu(os.Stdout)
	if err != nil {
		printError(err.Error())
	}
	if cfg, err := config.Load(); err == nil && saved {
		s.cfg.Settings = cfg.Settings
		if url := cfg.ResolveURL(globalURL); url != s.baseURL {
			s.baseURL, s.client.BaseURL = url, url
			s.reset()
		}
	}
	if saved || err != nil {
		fmt.Println()
	}
}

func (s *chatSession) help(string) {
	var b strings.Builder
	b.WriteString("Commands\n")
	for _, c := range chatCommands {
		name := "/" + c.name
		if c.args != "" {
			name += " " + c.args
		}
		fmt.Fprintf(&b, "  %-18s %s\n", name, display.Muted(c.desc))
	}
	b.WriteString("\nKeys\n")
	for _, k := range [][2]string{
		{"!cmd", "run a command, its output goes with your next message (!!cmd: not sent)"},
		{"enter", "send"},
		{"ctrl+j, alt+enter", "new line (or end the line with \\)"},
		{"↑ ↓", "move between lines, browse earlier messages"},
		{"ctrl+g", "edit the message in $EDITOR"},
		{"ctrl+c", "stop the answer, clear the input; twice to quit"},
		{"ctrl+d", "quit"},
	} {
		fmt.Fprintf(&b, "  %-18s %s\n", k[0], display.Muted(k[1]))
	}
	fmt.Println(b.String())
}
