package cmd

import (
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
question, when none is given). When stdout is not a terminal, only the answer
is written to it, as plain text.

Example usage:
    docsgpt-cli ask "How do I open a file in Python?"
    tail -n 50 app.log | docsgpt-cli ask "Why does this fail?"
    docsgpt-cli ask "Summarize the README" > summary.md

On a terminal, the first bash/sh code block of the answer is copied to your clipboard.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		piped, err := readPipedStdin()
		if err != nil {
			return err
		}
		question := strings.Join(args, " ")
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

// readPipedStdin returns what was piped or redirected into stdin. A terminal,
// or a device such as /dev/null, gives "".
func readPipedStdin() (string, error) {
	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeNamedPipe == 0 && !fi.Mode().IsRegular()) {
		return "", nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}
