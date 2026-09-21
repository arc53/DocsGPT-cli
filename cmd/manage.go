package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/manage"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Exit codes shared by the account-level commands; they mirror `bench`:
// 0 ok, 1 the operation failed, 2 usage / configuration / validation error.
const (
	exitFailure = 1
	exitUsage   = 2
)

// exitError carries a specific process exit code up to Execute.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// usageErr marks err as a usage/validation problem (exit code 2).
func usageErr(err error) error {
	if err == nil {
		return nil
	}
	return &exitError{code: exitUsage, err: err}
}

func usageErrf(format string, args ...any) error {
	return usageErr(fmt.Errorf(format, args...))
}

// exitCodeFor maps a command error to the process exit code.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) && ee.code != 0 {
		return ee.code
	}
	return exitFailure
}

// usageArgs wraps a positional-args validator so its failures exit with 2.
func usageArgs(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		return usageErr(v(cmd, args))
	}
}

// markManagement applies the conventions shared by the account-level command
// trees: flag errors exit with 2, usage is not dumped on runtime failures,
// and the startup banner is skipped (their stdout is data: tables, JSON, YAML).
// Call it after the subcommands are attached: SilenceUsage is per command.
func markManagement(c *cobra.Command) {
	c.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr(err) })
	silenceUsage(c)
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[noBannerAnnotation] = "true"
}

func silenceUsage(c *cobra.Command) {
	c.SilenceUsage = true
	for _, sub := range c.Commands() {
		silenceUsage(sub)
	}
}

const noBannerAnnotation = "docsgpt/no-banner"

// hasNoBanner reports whether cmd or one of its parents opted out of the banner.
func hasNoBanner(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[noBannerAnnotation] == "true" {
			return true
		}
	}
	return false
}

// userAgent is the User-Agent of account-level requests.
func userAgent() string { return "docsgpt-cli/" + Version }

// newManageClient resolves the base URL (--url > DOCSGPT_URL > config) and the
// personal access token (--token > DOCSGPT_TOKEN > config) into a client.
func newManageClient() (*manage.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, usageErrf("load config: %w", err)
	}
	token, _ := cfg.ResolveToken(globalToken)
	if token == "" {
		return nil, usageErrf("no personal access token configured: run 'docsgpt-cli login', set %s, or pass --token", config.EnvToken)
	}
	return manage.New(cfg.ResolveURL(globalURL), token, userAgent()), nil
}

// writeJSON pretty-prints a raw server document (or any value) to w.
func writeJSON(w io.Writer, v any) error {
	if raw, ok := v.(json.RawMessage); ok {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err == nil {
			buf.WriteByte('\n')
			_, err = w.Write(buf.Bytes())
			return err
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// confirmDestructive asks for a y/N confirmation on a terminal. Without a
// terminal (CI) it refuses and asks for --yes, so a pipeline never hangs on a
// prompt and never deletes by accident.
func confirmDestructive(in io.Reader, out io.Writer, interactive bool, question string) error {
	if !interactive {
		return usageErrf("%s — refusing without confirmation: pass --yes", question)
	}
	fmt.Fprintf(out, "%s [y/N]: ", question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return &exitError{code: exitFailure, err: errors.New("aborted")}
}

func stdinIsTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

// textOrDash renders empty table cells.
func textOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// anyText renders loosely typed server fields (dates, token counts).
func anyText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		return fmt.Sprint(t)
	}
}

func warnLine(w io.Writer, msg string) {
	fmt.Fprintln(w, display.Warn("warning:"), msg)
}
