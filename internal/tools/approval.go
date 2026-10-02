package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// Session runs the model's tool calls for one chat session or ask run.
// Approval is the only gate: every command and write is shown and asked
// about, unless AutoApprove is set or the user chose "Always allow" for it
// earlier in the session. Reads ask only outside the working directory or
// for files that may hold secrets (readReason).
type Session struct {
	AutoApprove bool          // run every call unasked: --auto-approve, "Always approve", /approve
	Timeout     time.Duration // per command or read

	allowWrites bool
	allowReads  bool
	allowed     map[string]bool // keys of always-allowed commands
}

// Handle runs one tool call and returns the result for the model. A
// Ctrl+C (or Esc) at the approval prompt calls cancel, which stops the run.
func (s *Session) Handle(ctx context.Context, cancel context.CancelFunc, tc docsgpt.ToolCall) string {
	if ctx.Err() != nil {
		return "The user interrupted the run before this tool call ran."
	}
	var args struct {
		Command          string `json:"command"`
		WorkingDirectory string `json:"working_directory"`
		Path             string `json:"path"`
		Content          string `json:"content"`
		Offset           int    `json:"offset"`
		Limit            int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return "Error: invalid arguments: " + err.Error()
	}
	switch name := NormalizeName(tc.Function.Name); name {
	case "run_command":
		return s.runCommand(ctx, cancel, args.Command, args.WorkingDirectory)
	case "read_file":
		return s.readFile(ctx, cancel, args.Path, args.Offset, args.Limit)
	case "write_file":
		return s.writeFile(ctx, cancel, args.Path, args.Content)
	default:
		return "Error: unknown tool " + name
	}
}

func (s *Session) runCommand(ctx context.Context, cancel context.CancelFunc, command, dir string) string {
	var note string
	if dir != "" {
		note = "in " + dir
	}
	edited, titled := false, false
	for {
		if !titled {
			display.ToolTitle("$ "+command, note)
			titled = true
		}
		if s.AutoApprove || s.allowed[alwaysKey(command)] {
			break
		}
		var always *ui.Item
		if key := display.Safe(alwaysKey(command)); key != "" {
			always = &ui.Item{Label: "Always allow " + key, Description: "run " + key + " commands without asking this session"}
		}
		choice, refusal := s.ask(cancel, "run once", always, true)
		if refusal != "" {
			return refusal
		}
		if choice == "edit" {
			// The one-line field shows the command as the title does; what
			// the user submits is what runs, with ␊ read back as a newline.
			shown := display.Safe(command)
			v, err := ui.Input{Title: "Edit command", Value: shown, Stderr: true, Summary: func(string) string { return "" }}.Run()
			if v = strings.TrimSpace(v); err == nil && v != "" && v != shown {
				command, edited, titled = strings.ReplaceAll(v, "␊", "\n"), true, false
			}
			continue // ask again, about the edited command
		}
		if choice == "always" {
			if s.allowed == nil {
				s.allowed = map[string]bool{}
			}
			s.allowed[alwaysKey(command)] = true
		}
		break
	}

	view := display.NewTailView()
	var out tailBuffer
	start := time.Now()
	err := runCommand(ctx, command, dir, s.Timeout, io.MultiWriter(&out, view))
	view.Close()
	took := display.Duration(time.Since(start))

	result := out.String()
	if strings.TrimSpace(result) == "" {
		result = "(no output)"
	}
	if edited {
		result = fmt.Sprintf("[The user edited the command to: %s]\n%s", command, result)
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		display.ToolStatus(true, "exit 0 · "+took)
		return result
	case errors.Is(err, context.Canceled):
		display.ToolStatus(false, "cancelled")
		return result + "\n[The user interrupted the command.]"
	case errors.Is(err, errTimeout):
		display.ToolStatus(false, "timed out after "+display.Duration(s.Timeout))
		return result + fmt.Sprintf("\n[The command timed out after %s.]", display.Duration(s.Timeout))
	case errors.As(err, &exit):
		display.ToolStatus(false, fmt.Sprintf("exit %d · %s", exit.ExitCode(), took))
		return result + fmt.Sprintf("\n[The command exited with code %d.]", exit.ExitCode())
	}
	display.ToolStatus(false, err.Error())
	return result + "\n[The command failed: " + err.Error() + "]"
}

func (s *Session) readFile(ctx context.Context, cancel context.CancelFunc, path string, offset, limit int) string {
	title := "read " + display.ShortPath(path)
	if offset > 1 || limit > 0 {
		title += fmt.Sprintf(":%d", max(offset, 1))
		if limit > 0 {
			title += fmt.Sprintf("-%d", max(offset, 1)+limit-1)
		}
	}
	real, err := regularFile(path)
	if err != nil {
		display.ToolTitle(title, "")
		display.ToolStatus(false, err.Error())
		return "Error: " + err.Error()
	}
	var reason string
	if !s.AutoApprove && !s.allowReads {
		reason = readReason(real)
	}
	var note string
	if reason != "" {
		note = "(" + reason + ")"
		// Show where a symlink leads (one above the working directory,
		// like /tmp on macOS, is not worth it).
		given := path
		if cwd, err := os.Getwd(); err == nil && !filepath.IsAbs(path) {
			if c, err := filepath.EvalSymlinks(cwd); err == nil {
				cwd = c
			}
			given = filepath.Join(cwd, path)
		}
		if filepath.Clean(given) != real {
			note = "→ " + display.ShortPath(real) + " " + note
		}
	}
	display.ToolTitle(title, note)
	if reason != "" {
		choice, refusal := s.ask(cancel, "read this file once",
			&ui.Item{Label: "Always allow reads", Description: "read any file without asking this session"}, false)
		if refusal != "" {
			return refusal
		}
		s.allowReads = choice == "always"
	}

	readCtx := ctx
	if s.Timeout > 0 {
		var stop context.CancelFunc
		readCtx, stop = context.WithTimeout(ctx, s.Timeout)
		defer stop()
	}
	text, shown, total, err := readFile(readCtx, real, offset, limit)
	switch {
	case ctx.Err() != nil:
		display.ToolStatus(false, "cancelled")
		return "The user interrupted the read."
	case errors.Is(err, context.DeadlineExceeded):
		display.ToolStatus(false, "timed out after "+display.Duration(s.Timeout))
		return "Error: reading the file timed out."
	case err != nil:
		display.ToolStatus(false, err.Error())
		return "Error: " + err.Error()
	}
	status := lines(shown)
	if shown < total {
		status += fmt.Sprintf(" of %d", total)
	}
	display.ToolStatus(true, status)
	return text
}

// readReason says why reading the file at real (as resolved by
// regularFile) needs the user's approval, or "" when it does not: the
// file lies inside the working directory, which is not the home directory
// or above it, and nothing on the way there looks like it holds secrets.
func readReason(real string) string {
	cwd, err := os.Getwd()
	if err == nil {
		cwd, err = filepath.EvalSymlinks(cwd)
	}
	if err != nil {
		return "outside the working directory"
	}
	rel, err := filepath.Rel(cwd, real)
	if err != nil || !filepath.IsLocal(rel) {
		return "outside the working directory"
	}
	// Compared as files: on macOS and Windows "cd /users/me" is the home
	// directory under another spelling, which the path keeps.
	if home, err := os.UserHomeDir(); err == nil {
		if wd, err := os.Stat(cwd); err == nil {
			for d := home; ; d = filepath.Dir(d) {
				if fi, err := os.Stat(d); err == nil && os.SameFile(fi, wd) {
					return "the working directory holds your home directory"
				}
				if filepath.Dir(d) == d {
					break
				}
			}
		}
	}
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		if secretName(name) {
			return "may hold secrets"
		}
	}
	return ""
}

// secretNames are files and directories that commonly hold credentials.
var secretNames = map[string]bool{
	".ssh": true, ".gnupg": true, ".aws": true, ".azure": true, ".kube": true, ".docker": true,
	".docsgpt": true, ".password-store": true, "gcloud": true, ".netrc": true, "_netrc": true,
	".npmrc": true, ".yarnrc.yml": true, ".pypirc": true, ".pgpass": true, ".my.cnf": true, ".mylogin.cnf": true,
	".htpasswd": true, ".vault-token": true, ".envrc": true, ".dockercfg": true, ".s3cfg": true, ".boto": true,
	".terraformrc": true, "terraform.rc": true, ".authinfo": true, "auth.json": true,
	"secrets": true, ".secrets": true, ".git-credentials": true, "credentials": true, "credentials.json": true,
	"credentials.toml": true, "credentials.db": true, "application_default_credentials.json": true,
}

// secretName reports whether a file or directory name suggests secrets:
// .env files, keys and certificates, shell histories.
func secretName(name string) bool {
	name = strings.ToLower(name)
	switch ext := filepath.Ext(name); {
	case secretNames[name], name == ".env", strings.HasPrefix(name, ".env."), ext == ".env",
		strings.HasPrefix(name, "secrets."), strings.HasPrefix(name, "client_secret"),
		strings.HasPrefix(name, "id_") && ext == "", strings.HasSuffix(name, "_history"):
		return true
	default:
		return slices.Contains([]string{".pem", ".key", ".p12", ".pfx", ".p8", ".jks", ".keystore", ".kdbx",
			".keychain", ".keychain-db", ".ppk", ".gpg", ".asc", ".ovpn", ".tfstate", ".tfvars"}, ext)
	}
}

// maxPreviewBytes is the largest existing file a write shows a diff of.
const maxPreviewBytes = 1 << 20

func (s *Session) writeFile(ctx context.Context, cancel context.CancelFunc, path, content string) string {
	title := "write " + display.ShortPath(path)
	fi, err := os.Stat(path)
	if err == nil && !fi.Mode().IsRegular() {
		display.ToolTitle(title, "")
		display.ToolStatus(false, path+" is not a regular file")
		return "Error: " + path + " is not a regular file"
	}
	var before []byte
	if err == nil && fi.Size() <= maxPreviewBytes {
		before, _ = os.ReadFile(path)
	}
	preview, added, removed := display.DiffPreview(string(before), content, 10)
	switch {
	case errors.Is(err, os.ErrNotExist):
		display.ToolTitle(title, "(new, "+lines(added)+")")
	case err == nil && fi.Size() > maxPreviewBytes:
		preview = nil
		display.ToolTitle(title, fmt.Sprintf("(replaces %d bytes with %s)", fi.Size(), lines(added)))
	default:
		display.ToolTitle(title, fmt.Sprintf("(+%d −%d)", added, removed))
	}
	display.ToolLines(preview)

	if !s.AutoApprove && !s.allowWrites {
		choice, refusal := s.ask(cancel, "write this file once",
			&ui.Item{Label: "Always allow writes", Description: "write any file without asking this session"}, false)
		if refusal != "" {
			return refusal
		}
		s.allowWrites = choice == "always"
	}
	if ctx.Err() != nil {
		display.ToolStatus(false, "cancelled")
		return "The user interrupted the run before this tool call ran."
	}
	if err := writeFile(path, content); err != nil {
		display.ToolStatus(false, err.Error())
		return "Error: " + err.Error()
	}
	display.ToolStatus(true, "wrote "+lines(len(splitLines(content))))
	return fmt.Sprintf("Wrote %d bytes to %s.", len(content), path)
}

// ask shows the approval choices under the tool's title: Approve (once
// describes it), the narrower "Always allow" when there is one, Always
// approve, Deny and, with edit, Edit. It returns "approve", "always" or
// "edit", or the result for the model when the call must not run: denied,
// or cancelled (which also cancels the run). Always approve turns on
// AutoApprove and returns "approve".
func (s *Session) ask(cancel context.CancelFunc, once string, always *ui.Item, edit bool) (choice, refusal string) {
	items := []ui.Item{{Label: "Approve", Value: "approve", Keys: []string{"a", "y"}, Description: once}}
	if always != nil {
		always.Value, always.Keys = "always", []string{"l"}
		items = append(items, *always)
	}
	items = append(items,
		ui.Item{Label: "Always approve", Value: "all", Keys: []string{"p"}, Description: "run every tool call without asking this session"},
		ui.Item{Label: "Deny", Value: "deny", Keys: []string{"d", "n"}, Description: "don't run it, tell the model you declined"})
	if edit {
		items = append(items, ui.Item{Label: "Edit", Value: "edit", Keys: []string{"e"}, Description: "change the command, then decide again"})
	}
	choice, err := ui.Select{Items: items, Inline: true, Stderr: true, Summary: func(ui.Item) string { return "" }}.Run()
	switch {
	case errors.Is(err, ui.ErrCancelled):
		cancel()
		display.ToolStatus(false, "cancelled")
		return "", "The user interrupted the run before this tool call ran."
	case err != nil:
		display.ToolStatus(false, "not run: "+err.Error())
		return "", "The tool call could not be approved: " + err.Error()
	case choice == "deny":
		display.ToolStatus(false, "denied")
		return "", "The user denied this tool call."
	case choice == "all":
		s.AutoApprove = true
		return "approve", ""
	}
	return choice, ""
}

// lines counts lines for a status: "1 line", "3 lines".
func lines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}
