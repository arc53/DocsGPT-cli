package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	ctxenrich "github.com/arc53/DocsGPT-cli/internal/context"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var askCmd = &cobra.Command{
	Use:    "ask [question]",
	Hidden: true,
	Short:  "Ask a question to DocsGPT",
	Long: `Ask a question to DocsGPT, and instantly find answers about anything.

Anything piped into the command is sent along with the question (or is the
question, when none is given), up to 1 MB: unless stdin is a terminal or a
device such as /dev/null, it is read to its end. Where stdin stays open
without input (ssh, CI jobs, a "while read" loop), pass --no-stdin or
redirect it from /dev/null. When stdout is not a terminal, only the answer is
written to it, as plain text (terminal control sequences removed, as on a
terminal).

Example usage:
    docsgpt-cli ask "How do I open a file in Python?"
    tail -n 50 app.log | docsgpt-cli ask "Why does this fail?"
    docsgpt-cli ask "Summarize the README" > summary.md

On a terminal, the first bash/sh code block of the answer is copied to your clipboard.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		question := strings.Join(args, " ")
		var piped string
		if !globalNoStdin {
			var hint io.Writer
			if isatty.IsTerminal(os.Stderr.Fd()) {
				hint = os.Stderr
			}
			var err error
			if piped, err = readPipedStdin(os.Stdin, hint); err != nil {
				return err
			}
		}
		switch {
		case piped == "":
		case question == "":
			question = piped
		default:
			question += "\n\n<stdin>\n" + piped + "\n</stdin>"
		}
		if strings.TrimSpace(question) == "" {
			return usageErrf("please provide a question (as arguments or on stdin)")
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
		client := docsgpt.NewClient(baseURL, apiKey)

		fullQuestion := question
		if !globalNoContext {
			fullQuestion = ctxenrich.Prepend(ctxenrich.Build(cfg.Settings), question)
		}

		messages := []docsgpt.Message{
			{Role: "user", Content: fullQuestion},
		}

		tty := isatty.IsTerminal(os.Stdout.Fd())
		if tty {
			cwd, _ := os.Getwd()
			fmt.Println(display.RenderHeader("", keyName, baseURL, cwd))
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		// Tool calls need a person on stdin to approve them.
		var toolDefs []docsgpt.Tool
		if !globalNoTools && (globalAutoApprove || stdinIsTerminal()) {
			toolDefs = tools.ToolDefinitions()
		}
		toolSession := &tools.Session{AutoApprove: globalAutoApprove, Timeout: time.Duration(globalTimeout) * time.Second}

		renderer := display.NewStreamRenderer()

		onDelta := func(delta docsgpt.Delta, finishReason string) {
			renderer.Delta(delta)
		}

		onToolCall := func(tc docsgpt.ToolCall) string {
			renderer.Flush()
			defer renderer.Wait()
			ui.DiscardInput() // typed ahead, it would answer the approval
			return toolSession.Handle(ctx, cancel, tc)
		}

		renderer.Wait()
		if tty {
			// Echoed typing would shift the cursor under the redraws.
			defer ui.HoldInput()()
		}
		res, err := client.RunWithTools(ctx, messages, docsgpt.RunOptions{
			Tools: toolDefs, Stream: !globalNoStream, OnDelta: onDelta, OnToolCall: onToolCall,
		})
		renderer.Flush()
		if err != nil {
			if ctx.Err() != nil {
				return &exitError{code: 130, err: errors.New("interrupted")}
			}
			return err
		}
		if !tty {
			return nil
		}

		display.PrintSources(res.Sources)
		if command := extractCommand(renderer.Content()); command != "" {
			copyToClipboard(command)
		}
		return nil
	},
}

// maxStdin caps what is read from stdin.
const maxStdin = 1 << 20

// stdinHint is how long stdin may stay silent before the user is told that
// the command waits for it.
var stdinHint = time.Second

// readPipedStdin reads what was piped or redirected into f to its end, or
// returns "" for a terminal or a device such as /dev/null. When nothing has
// come after stdinHint, it says so on hint (stderr when that is a terminal,
// else nil): ssh, CI jobs and "while read" loops leave stdin open, and
// --no-stdin is for them.
func readPipedStdin(f *os.File, hint io.Writer) (string, error) {
	if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}
	type chunk struct {
		b   []byte
		err error
	}
	first := make(chan chunk, 1)
	go func() {
		b := make([]byte, 32<<10)
		n, err := f.Read(b)
		first <- chunk{b[:n], err}
	}()
	var c chunk
	select {
	case c = <-first:
	case <-time.After(stdinHint):
		if hint != nil {
			fmt.Fprint(hint, display.Muted("waiting for piped input… (--no-stdin to skip)"))
		}
		c = <-first
		if hint != nil {
			fmt.Fprint(hint, "\r\x1b[2K")
		}
	}
	if c.err != nil && c.err != io.EOF {
		return "", fmt.Errorf("read stdin: %w", c.err)
	}
	b, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(c.b), f), maxStdin+1))
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	text := string(b)
	if len(b) > maxStdin {
		text = strings.ToValidUTF8(string(b[:maxStdin]), "") + "\n[… truncated at 1 MB]"
		fmt.Fprintln(os.Stderr, display.Muted("stdin is over 1 MB; sending its first 1 MB"))
	}
	return strings.TrimSpace(text), nil
}
