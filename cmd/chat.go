package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	ctxenrich "github.com/arc53/DocsGPT-cli/internal/context"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/session"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:    "chat [message]",
	Short:  "Start an interactive chat session",
	Hidden: true,
	Long: `Start an interactive multi-turn chat session with DocsGPT.

The chat takes the whole window; the wheel, PgUp/PgDn and Shift+↑/↓ scroll
it, and text selected with the mouse is copied. Type / for the commands,
!cmd to run a shell command and send its output with your next message
(!!cmd to keep it to yourself). Enter sends, Shift+Enter, Ctrl+J or
Alt+Enter (or a trailing \) starts a new line, ↑/↓ browse earlier prompts
and Ctrl+G edits the message in $EDITOR. Esc or Ctrl+C stops an answer;
Ctrl+C clears the input, twice on an empty input (or Ctrl+D) quits.`,
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

	scr  *ui.Screen
	ctx  context.Context // ends with the chat
	said bool            // something was asked or shown again: the transcript is printed on exit
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
		{name: "approve", desc: "Run tool calls without asking, or ask again", run: (*chatSession).toggleApprove},
		{name: "key", desc: "Switch to another agent API key", run: (*chatSession).switchKey},
		{name: "settings", desc: "Change settings", run: (*chatSession).settings},
		{name: "help", desc: "Show commands and keys", run: (*chatSession).help},
		{name: "quit", desc: "Leave the chat", aliases: []string{"exit"}, run: func(s *chatSession, _ string) { s.quit = true }},
	}
}

func runChat(first string) error {
	if !ui.Interactive() {
		return ui.ErrNotInteractive
	}
	if os.Getenv("TERM") == "dumb" {
		return errors.New(`the chat needs a terminal that can move the cursor (TERM is "dumb"); ask one question at a time instead: docsgpt-cli "question"`)
	}
	if chatResume {
		cwd, _ := os.Getwd()
		if list, err := session.List(cwd); err != nil || len(list) == 0 {
			fmt.Println(display.Dim("No earlier chat in this directory."))
			return nil
		}
	}
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
	var commands []ui.Command
	for _, c := range chatCommands {
		commands = append(commands, ui.Command{Name: c.name, Description: c.desc})
	}
	s.scr = ui.NewScreen(ui.ScreenOptions{
		Commands: commands,
		History:  ui.LoadHistory(filepath.Join(config.Dir(), "history")),
		Mouse:    cfg.Settings.Mouse != "off",
	})
	s.tools.UI = &screenTools{scr: s.scr}

	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	done := make(chan any, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.scr.Kill()
				done <- fmt.Sprintf("%v\n\n%s", r, debug.Stack())
				return
			}
			done <- nil
		}()
		s.loop(first)
		s.scr.Quit()
	}()
	err = s.scr.Run()
	cancel() // whatever still runs
	select {
	case r := <-done:
		if r != nil {
			panic(r) // the terminal is restored
		}
	case <-time.After(3 * time.Second):
	}

	var sig ui.Signal
	hup := errors.As(err, &sig) && sig.Signal == syscall.SIGHUP // no terminal left
	if s.said && !hup {
		width, _, werr := term.GetSize(os.Stdout.Fd())
		if werr != nil {
			width = 80
		}
		fmt.Println(s.scr.Transcript(width))
	}
	if s.sess.Saved() && !hup {
		fmt.Println(display.Dim("Continue this chat with: docsgpt-cli -c"))
	}
	if errors.As(err, &sig) {
		return sig
	}
	return err
}

// loop starts the chat (-r, -c, a first message), then handles what the
// user sends until the screen ends or they quit.
func (s *chatSession) loop(first string) {
	switch {
	case chatResume:
		if !s.pickSession() {
			return
		}
	case chatContinue:
		cwd, _ := os.Getwd()
		if list, _ := session.List(cwd); len(list) > 0 {
			s.resume(list[0])
			break
		}
		s.top()
		s.note("No earlier chat in this directory; starting a new one.")
	default:
		s.top()
	}
	s.footer()
	if first = strings.TrimSpace(first); first != "" {
		s.send(first, first)
	}
	for !s.quit {
		s.footer()
		text, shown, ok := s.scr.Next()
		if !ok {
			return
		}
		s.handle(text, shown)
	}
}

// footer sets the line under the input: where (left), as whom and what is
// on (right).
func (s *chatSession) footer() {
	cwd, _ := os.Getwd()
	status := []string{s.keyName, hostOf(s.baseURL)}
	if n := len(s.shellOutput); n > 0 {
		status = append(status, fmt.Sprintf("+%d command %s", n, plural(n, "output", "outputs")))
	}
	if s.showReasoning {
		status = append(status, "think on")
	}
	if s.tools.AutoApprove { // last: the footer is dim up to it
		status = append(status, display.Warn("auto-approve"))
	}
	right := strings.Join(status, " · ")
	s.scr.Footer(display.ChatFooter(cwd, right), right)
}

// top starts the transcript over with the chat's header.
func (s *chatSession) top() {
	var files []string
	if !globalNoContext {
		files = ctxenrich.Files(s.cfg.Settings)
	}
	s.scr.Clear()
	s.scr.Add(display.Header(Version, s.newline, files, s.cfg.Settings.Banner))
}

func (s *chatSession) note(msg string) { s.scr.Add(display.Note(msg)) }
func (s *chatSession) fail(msg string) { s.scr.Add(display.Failure(msg)) }

// writer adds what is written to it to the transcript.
func (s *chatSession) writer() io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		s.scr.Add(display.Text(string(p)))
		return len(p), nil
	})
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// screenTools shows the tool calls in the transcript and asks about them in
// the screen's panel.
type screenTools struct {
	scr   *ui.Screen
	block *display.ToolBlock
}

func (t *screenTools) Open(title, note string) {
	if t.block != nil {
		t.block.Close(true, "")
	}
	t.block = display.NewToolBlock(title, note)
	t.scr.Add(t.block)
}

func (t *screenTools) Lines(lines []string) {
	t.block.AddLines(lines)
	t.scr.Changed()
}

func (t *screenTools) Output() io.Writer {
	t.scr.Status("Running…")
	block := t.block
	return writerFunc(func(p []byte) (int, error) {
		block.Write(p)
		t.scr.Changed()
		return len(p), nil
	})
}

func (t *screenTools) Close(ok bool, status string) {
	t.block.Close(ok, status)
	t.block = nil
	t.scr.Changed()
}

func (t *screenTools) Choose(items []ui.Item) (string, error) {
	return t.scr.Select(ui.Select{Items: items, Inline: true})
}

func (t *screenTools) Edit(title, value string) (string, error) {
	return t.scr.Input(ui.Input{Title: title, Value: value})
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
	s.fail("Unknown command /" + name + " (type / to see them)")
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
	s.said = true
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	s.scr.Busy("Running…", cancel)
	defer s.scr.Busy("", nil)
	out := tools.RunShell(ctx, s.tools.UI, command)
	if keep {
		s.shellOutput = append(s.shellOutput, fmt.Sprintf("I ran `%s`:\n```\n%s\n```", command, strings.TrimRight(out, "\n")))
	}
}

// send asks the agent, showing the message, the answer and its sources.
func (s *chatSession) send(text, shown string) {
	s.said = true
	s.scr.Add(display.User(shown))

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

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	s.scr.Busy("Thinking…", cancel)
	defer s.scr.Busy("", nil)
	answer := func() *display.Answer {
		a := display.NewAnswer(s.showReasoning)
		s.scr.Add(a)
		return a
	}
	ans := answer()
	var texts []string
	label := "Thinking…"
	res, err := s.client.RunWithTools(ctx, messages, docsgpt.RunOptions{
		Tools: s.toolDefs, Stream: !globalNoStream, ConversationID: s.conversationID,
		OnDelta: func(delta docsgpt.Delta, _ string) {
			if ans.Delta(delta) {
				if delta.Content != "" && label != "Answering…" {
					label = "Answering…"
					s.scr.Status(label)
				}
				s.scr.Changed()
			}
		},
		OnToolCall: func(tc docsgpt.ToolCall) string {
			ans.Finish()
			texts = append(texts, ans.Content())
			out := s.tools.Handle(ctx, cancel, tc)
			s.footer() // "Always approve" turns auto-approve on
			label = "Thinking…"
			s.scr.Status(label)
			ans = answer()
			return out
		},
	})
	ans.Finish()
	s.scr.Changed()
	texts = append(texts, ans.Content())
	if err != nil {
		if ctx.Err() != nil {
			s.note("Interrupted.")
			return
		}
		// The server may have refused the conversation (deleted, say):
		// the next turn starts a new one from the messages.
		s.conversationID = ""
		s.fail(err.Error())
		return
	}
	s.scr.Add(display.Sources(res.Sources))

	var added []session.Entry
	for i := range res.Messages[len(messages)-1:] {
		added = append(added, session.Entry{Message: &res.Messages[len(messages)-1+i]})
	}
	added[0].Text = text
	added[len(added)-1].Sources = res.Sources
	if err := s.sess.Record(s.baseURL, s.keyName, res.ConversationID, added...); err != nil {
		s.fail("Could not save the chat: " + err.Error())
	}
	s.history, s.conversationID = res.Messages, res.ConversationID
	s.sentContext, s.shellOutput = block, nil
	s.lastAnswer = strings.Join(slices.DeleteFunc(texts, func(t string) bool { return strings.TrimSpace(t) == "" }), "\n\n")
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
		s.note("No earlier chat in this directory.")
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
	v, err := s.scr.Select(ui.Select{Title: "Resume a chat", Items: items, Filter: true})
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
	s.said = true
	s.top()
	s.note(fmt.Sprintf("── resumed · %s · %d %s ──", display.Ago(sess.Updated), len(turns), plural(len(turns), "message", "messages")))
	for _, t := range turns {
		s.scr.Add(display.User(t.Question))
		s.scr.Add(display.Markdown(t.Answer))
		s.scr.Add(display.Sources(t.Sources))
		s.lastAnswer = display.StripControls(t.Answer)
	}
	if note != "" {
		s.scr.Add(display.Text(display.Warn("! ") + display.Dim(note)))
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
	s.note("Reasoning will be " + state + ".")
}

func (s *chatSession) toggleApprove(string) {
	s.tools.AutoApprove = !s.tools.AutoApprove
	msg := "Tool calls now need approval."
	if s.tools.AutoApprove {
		msg = "Tool calls run without asking."
	}
	s.note(msg)
}

// copyAnswer copies the last answer, or one of its code blocks.
func (s *chatSession) copyAnswer(string) {
	if s.lastAnswer == "" {
		s.fail("Nothing to copy yet.")
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
		v, err := s.scr.Select(ui.Select{Title: "Copy", Items: items})
		if err != nil {
			return
		}
		if i, _ := strconv.Atoi(v); i >= 0 {
			text = blocks[i].code
		}
	}
	msg, err := copyText(text, s.scr.Copy)
	if err != nil {
		s.fail(err.Error())
		return
	}
	s.scr.Add(display.Done(msg))
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
		s.fail("Nothing to export yet.")
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
			s.fail(shown + " is not a file.")
			return
		}
		if ok, _ := ui.ConfirmWith(s.scr, "Overwrite "+shown+"?", false); !ok {
			s.note("Not exported.")
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
		s.fail(err.Error())
		return
	}
	s.scr.Add(display.Done("Exported to " + shown))
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
	name, err := s.scr.Select(ui.Select{Title: "Chat with", Items: items, Default: current, Filter: len(items) > 8})
	if err != nil {
		return
	}
	if name == "\x00add" {
		cfg := s.cfg
		if name, err = addCredential(s.ctx, s.scr, &cfg, true, s.writer()); err != nil {
			if !errors.Is(err, ui.ErrCancelled) {
				s.fail(err.Error())
			}
			return
		}
		s.cfg.Keys = cfg.Keys
	}
	if name == s.keyName {
		return
	}
	s.keyName, s.client.APIKey = name, s.cfg.Keys[name]
	s.reset()
	s.scr.Add(display.Done("Chatting with " + name + ", in a new conversation."))
}

func (s *chatSession) settings(string) {
	saved, err := runConfigMenu(s.scr, s.writer())
	if err != nil {
		s.fail(err.Error())
	}
	if cfg, err := config.Load(); err == nil && saved {
		s.cfg.Settings = cfg.Settings
		s.scr.Mouse(cfg.Settings.Mouse != "off")
		if url := cfg.ResolveURL(globalURL); url != s.baseURL {
			s.baseURL, s.client.BaseURL = url, url
			s.reset()
		}
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
		{"wheel, pgup pgdn", "scroll (shift+↑ ↓ by a line)"},
		{"drag", "select text and copy it (double click: a word, triple: a line)"},
		{"ctrl+↑ ctrl+↓", "previous / next message of yours"},
		{"end, ctrl+end", "back to the end (home, ctrl+home: the start)"},
		{"esc", "clear the selection, stop the answer"},
		{"ctrl+c", "stop the answer, clear the input; twice to quit"},
		{"ctrl+d", "quit"},
		{"ctrl+z", "suspend (fg to come back)"},
	} {
		fmt.Fprintf(&b, "  %-18s %s\n", k[0], display.Muted(k[1]))
	}
	b.WriteString(display.Dim("The terminal's own selection: Shift-drag (Option-drag in iTerm2), or turn the mouse off in /settings."))
	s.scr.Add(display.Text(b.String()))
}
