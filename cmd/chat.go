package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	ctxenrich "github.com/arc53/DocsGPT-cli/internal/context"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/session"
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
your next message (!!cmd to keep it to yourself). Enter sends, Shift+Enter,
Ctrl+J or Alt+Enter (or a trailing \) starts a new line, ↑/↓ browse earlier prompts and
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
	sess        *session.Session
	lastAnswer  string

	newline       string // the key for a new line, for the header
	showReasoning bool
	toolDefs      []docsgpt.Tool
	tools         *tools.Session
	quit          bool
	stopped       error // TERM or HUP ended a request: the chat exits with it
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
		{name: "resume", desc: "Resume an earlier chat in this directory", run: func(s *chatSession, _ string) { s.pickSession() }},
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
		client:  docsgpt.NewClient(baseURL, apiKey),
		tools:   &tools.Session{AutoApprove: globalAutoApprove, Timeout: time.Duration(globalTimeout) * time.Second},
		newline: ui.NewlineKey(),
	}
	s.reset()
	if !globalNoTools {
		s.toolDefs = tools.ToolDefinitions()
	}

	// -r picks the chat first, inline: leaving the picker leaves the
	// window as it was.
	switch {
	case chatResume:
		if !s.pickSession() {
			return nil
		}
	case chatContinue:
		cwd, _ := os.Getwd()
		if list, _ := session.List(cwd); len(list) > 0 {
			s.resume(list[0])
			break
		}
		s.top()
		fmt.Println(display.Dim("No earlier chat in this directory; starting a new one.") + "\n")
	default:
		s.top()
	}

	if first = strings.TrimSpace(first); first != "" {
		s.send(first, first)
	}
	editor := &ui.Editor{History: ui.LoadHistory(filepath.Join(config.Dir(), "history")), Pin: true}
	for _, c := range chatCommands {
		editor.Commands = append(editor.Commands, ui.Command{Name: c.name, Description: c.desc})
	}
	for !s.quit && s.stopped == nil {
		editor.Footer, editor.Status = s.footer()
		text, shown, err := editor.Run()
		if errors.Is(err, io.EOF) {
			break
		}
		if err == nil {
			s.handle(text, shown)
		} else if !errors.As(err, new(ui.Signal)) {
			return err
		}
		// TERM or HUP at a prompt: this one, or a command's picker.
		s.stopped = cmp.Or(s.stopped, ui.Stopped())
	}
	if s.sess.Saved() {
		fmt.Println(display.Dim("Continue this chat with: docsgpt-cli -c"))
	}
	return s.stopped
}

// footer returns the text under the input: where (left), as whom and
// what is on (right).
func (s *chatSession) footer() (left, right string) {
	cwd, _ := os.Getwd()
	status := []string{s.keyName, hostOf(s.baseURL)}
	if n := len(s.shellOutput); n > 0 {
		status = append(status, fmt.Sprintf("+%d command %s", n, plural(n, "output", "outputs")))
	}
	if s.showReasoning {
		status = append(status, "think on")
	}
	right = strings.Join(status, " · ")
	return display.ChatFooter(cwd, right), right
}

// top starts the window over with the chat's header: what it showed goes
// to the scrollback.
func (s *chatSession) top() {
	if ui.Interactive() {
		display.ClaimScreen()
	}
	display.ShowBanner(s.cfg.Settings.Banner)
	var files []string
	if !globalNoContext {
		files = ctxenrich.Files(s.cfg.Settings)
	}
	fmt.Println(display.ChatHeader(Version, s.newline, files))
}

// handle acts on one submitted input: a command, a shell command or a
// message for the agent. What the user saw decides, so a collapsed paste
// starting with ! or / is a message.
func (s *chatSession) handle(text, shown string) {
	line, seen := strings.TrimSpace(text), strings.TrimSpace(shown)
	switch {
	case strings.HasPrefix(seen, "!"):
		s.shell(line)
	case strings.HasPrefix(seen, "/") && s.command(line):
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
	ctx, stop := signalContext()
	defer stop()
	// The tool block opens with a blank line, which the input already left.
	fmt.Fprint(os.Stderr, "\x1b[1A")
	restore := ui.HoldInput()
	defer restore()
	out := tools.RunShell(ctx, command)
	restore()
	if s.stopped = terminated(ctx); s.stopped != nil {
		return
	}
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
	ctx, stop := signalContext()
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
		if s.stopped = terminated(ctx); s.stopped != nil {
			return
		}
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

	var added []session.Entry
	for i := range res.Messages[len(messages)-1:] {
		added = append(added, session.Entry{Message: &res.Messages[len(messages)-1+i]})
	}
	added[0].Text = text
	added[len(added)-1].Sources = res.Sources
	if err := s.sess.Record(s.baseURL, s.keyName, res.ConversationID, added...); err != nil {
		printError("Could not save the chat: " + err.Error())
		fmt.Println()
	}
	s.history, s.conversationID = res.Messages, res.ConversationID
	s.sentContext, s.shellOutput = block, nil
	s.lastAnswer = renderer.Content()
}

// reset starts a new conversation, saved in a new session.
func (s *chatSession) reset() {
	s.history, s.conversationID, s.sentContext = nil, "", ""
	s.shellOutput, s.lastAnswer = nil, ""
	cwd, _ := os.Getwd()
	s.sess = session.New(cwd, s.baseURL, s.keyName)
}

// pickSession asks which earlier chat of this directory to resume and
// resumes it. It reports whether one was chosen.
func (s *chatSession) pickSession() bool {
	cwd, _ := os.Getwd()
	list, err := session.List(cwd)
	if err != nil || len(list) == 0 {
		fmt.Println(display.Dim("No earlier chat in this directory.") + "\n")
		return false
	}
	items := make([]ui.Item, len(list))
	for i, sess := range list {
		n := len(sess.Turns())
		items[i] = ui.Item{
			Label:       display.Safe(sess.Title()),
			Value:       strconv.Itoa(i),
			Description: fmt.Sprintf("%s · %d %s · %s", display.Ago(sess.Updated), n, plural(n, "message", "messages"), sess.Key),
		}
	}
	v, err := ui.Select{Title: "Resume a chat", Items: items, Filter: true, Summary: func(ui.Item) string { return "" }}.Run()
	if err != nil {
		return false
	}
	i, _ := strconv.Atoi(v)
	s.resume(list[i])
	return true
}

// resume continues sess: its messages become the history, the server
// conversation goes on when the key and server are the same, and the last
// exchanges are shown again.
func (s *chatSession) resume(sess *session.Session) {
	s.reset()
	s.sess = sess
	for _, e := range sess.Messages {
		s.history = append(s.history, *e.Message)
		if c := e.Message.Content; e.Message.Role == "user" && strings.HasPrefix(c, "<context>") {
			if i := strings.Index(c, "</context>"); i > 0 {
				s.sentContext = c[:i+len("</context>")]
			}
		}
	}
	s.conversationID = sess.ConversationID
	if key, ok := s.cfg.Keys[sess.Key]; ok && sess.Key != s.keyName && globalKey == "" && os.Getenv(config.EnvAPIKey) == "" {
		s.keyName, s.client.APIKey = sess.Key, key
	}
	var note string
	switch {
	case sess.Server != s.baseURL:
		note = "This chat was on " + hostOf(sess.Server) + "; it goes on here in a new conversation."
	case sess.Key != s.keyName:
		note = "This chat was with key " + sess.Key + "; it goes on with " + s.keyName + " in a new conversation."
	}
	if note != "" {
		s.conversationID = ""
	}

	turns := sess.Turns()
	s.top()
	fmt.Println(display.Dim(fmt.Sprintf("── resumed · %s · %d %s ──", display.Ago(sess.Updated), len(turns), plural(len(turns), "message", "messages"))) + "\n")
	for _, t := range turns[max(0, len(turns)-3):] {
		display.UserMessage(t.Question)
		display.PrintMarkdown(t.Answer)
		display.PrintSources(t.Sources)
		fmt.Println()
		s.lastAnswer = display.StripControls(t.Answer)
	}
	if note != "" {
		fmt.Println(display.Warn("! ") + display.Dim(note) + "\n")
	}
}

func (s *chatSession) newConversation(string) {
	s.reset()
	s.top()
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
			items = append(items, ui.Item{Label: display.Safe(first), Value: strconv.Itoa(i), Description: desc})
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
	turns := s.sess.Turns()
	if len(turns) == 0 {
		printError("Nothing to export yet.")
		fmt.Println()
		return
	}
	if file == "" {
		file = "docsgpt-" + time.Now().Format("2006-01-02-150405") + ".md"
	}
	shown := file
	if strings.HasPrefix(file, "~/") || strings.HasPrefix(file, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			file = filepath.Join(home, file[2:])
		}
	}
	if fi, err := os.Stat(file); err == nil {
		if !fi.Mode().IsRegular() {
			printError(shown + " is not a file.")
			fmt.Println()
			return
		}
		if ok, _ := ui.Confirm("Overwrite "+shown+"?", false); !ok {
			fmt.Println(display.Dim("Not exported.") + "\n")
			return
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# DocsGPT chat · %s\n", time.Now().Format("2006-01-02 15:04"))
	for _, t := range turns {
		b.WriteString("\n## You\n\n" + strings.TrimSpace(t.Question) + "\n\n## DocsGPT\n\n" + strings.TrimSpace(t.Answer) + "\n")
		for i, src := range t.Sources {
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
	os.MkdirAll(filepath.Dir(file), 0o755)
	// The answers are the model's: no control sequences for a later cat.
	if err := os.WriteFile(file, []byte(display.StripControls(b.String())), 0o644); err != nil {
		printError(err.Error())
		fmt.Println()
		return
	}
	fmt.Println(display.Success("✓") + " " + display.Dim("Exported to "+shown) + "\n")
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
		{"shift+enter, ctrl+j", "new line (alt+enter too, or end the line with \\)"},
		{"↑ ↓", "move between lines, browse earlier messages"},
		{"ctrl+g", "edit the message in $EDITOR"},
		{"ctrl+c", "stop the answer, clear the input; twice to quit"},
		{"ctrl+d", "quit"},
	} {
		fmt.Fprintf(&b, "  %-18s %s\n", k[0], display.Muted(k[1]))
	}
	fmt.Println(b.String())
}
