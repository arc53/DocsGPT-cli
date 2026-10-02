package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/arc53/DocsGPT-cli/internal/display"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

func printError(message string) {
	display.ErrorMsg(message)
}

// codeBlock is a fenced code block of an answer.
type codeBlock struct{ lang, code string }

var fenceRe = regexp.MustCompile("(?ms)^```([^\\s`]*)[^\\n]*\\n(.*?)^```")

// codeBlocks lists the fenced code blocks of markdown text.
func codeBlocks(md string) []codeBlock {
	var out []codeBlock
	for _, m := range fenceRe.FindAllStringSubmatch(md, -1) {
		out = append(out, codeBlock{m[1], strings.TrimSuffix(m[2], "\n")})
	}
	return out
}

// extractCommand returns the first bash or sh block of an answer.
func extractCommand(answer string) string {
	for _, b := range codeBlocks(answer) {
		if b.lang == "bash" || b.lang == "sh" {
			return b.code
		}
	}
	return ""
}

// copyToClipboard copies command and says so in a dim line.
func copyToClipboard(command string) {
	command = strings.TrimSpace(command)
	if err := clipboard.WriteAll(command); err != nil {
		printError("Failed to copy to clipboard: " + err.Error())
		return
	}
	first, rest, multi := strings.Cut(command, "\n")
	first = ansi.Truncate(first, 50, "…")
	if multi {
		n := strings.Count(rest, "\n") + 1
		first += fmt.Sprintf(" (+%d more %s)", n, plural(n, "line", "lines"))
	}
	fmt.Println(display.Success("✓") + " " + display.Dim("Copied to clipboard: "+first))
}

// signalContext is cancelled by Ctrl+C, and by the TERM and HUP that
// timeout, kill or a closed terminal send, so the run ends through its
// deferred clean-ups (the terminal's echo) instead of dying mid-way.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case sig := <-ch:
			cancel(stopSignal{sig})
		case <-ctx.Done():
		}
	}()
	return ctx, func() { signal.Stop(ch); cancel(context.Canceled) }
}

// stopSignal is the cause of a signalContext a signal cancelled.
type stopSignal struct{ os.Signal }

func (s stopSignal) Error() string { return s.String() }

// terminated returns the error to exit with when TERM or HUP cancelled
// ctx (128 + the signal, as a shell reports it), else nil.
func terminated(ctx context.Context) error {
	var sig stopSignal
	if !errors.As(context.Cause(ctx), &sig) || sig.Signal == os.Interrupt {
		return nil
	}
	n, _ := sig.Signal.(syscall.Signal)
	return &exitError{code: 128 + int(n), err: sig}
}
