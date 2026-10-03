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
	UI          UI            // stderr when nil

	allowWrites bool
	allowReads  bool
	allowed     map[string]bool // keys of always-allowed commands
}

func (s *Session) ui() UI {
	if s.UI == nil {
		s.UI = &stderrUI{}
	}
	return s.UI
}

// Handle runs one tool call and returns the result for the model. A
// Ctrl+C (or Esc) at the approval prompt calls cancel, which stops the run.
func (s *Session) Handle(ctx context.Context, cancel context.CancelFunc, tc docsgpt.ToolCall) docsgpt.ToolResult {
	text := func(s string) docsgpt.ToolResult { return docsgpt.ToolResult{Content: s} }
	if ctx.Err() != nil {
		return text("The user interrupted the run before this tool call ran.")
	}
	var args callArgs
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return text("Error: invalid arguments: " + err.Error())
	}
	switch name := NormalizeName(tc.Function.Name); name {
	case "run_command":
		dir := args.WorkingDirectory
		if strings.TrimSpace(dir) != "" {
			dir = expandPath(dir)
		}
		return text(s.runCommand(ctx, cancel, args.Command, dir, time.Duration(args.Timeout)*time.Second))
	case "read_file":
		if strings.TrimSpace(args.Path) == "" {
			return text(s.fail("read", "no path given", "Error: path is required."))
		}
		return s.readFile(ctx, cancel, expandPath(args.Path), int(args.Offset), int(args.Limit))
	case "edit_file":
		if strings.TrimSpace(args.Path) == "" {
			return text(s.fail("edit", "no path given", "Error: path is required."))
		}
		edits := args.Edits
		if len(edits) == 0 && args.OldText != "" {
			edits = editList{{args.OldText, args.NewText}}
		}
		return text(s.editFile(ctx, cancel, expandPath(args.Path), edits))
	case "write_file":
		if strings.TrimSpace(args.Path) == "" {
			return text(s.fail("write", "no path given", "Error: path is required."))
		}
		return text(s.writeFile(ctx, cancel, expandPath(args.Path), args.Content))
	default:
		return text("Error: unknown tool " + name)
	}
}

// fail shows a call that cannot run, titled title, failed with status,
// and returns result.
func (s *Session) fail(title, status, result string) string {
	s.ui().Open(title, "")
	s.ui().Close(false, status)
	return result
}

// runCommand runs command in dir (the working directory when ""), for at
// most timeout when the model gave one (capped at maxTimeout seconds or
// the session's own timeout, whichever is longer), else s.Timeout.
func (s *Session) runCommand(ctx context.Context, cancel context.CancelFunc, command, dir string, timeout time.Duration) string {
	if strings.TrimSpace(command) == "" {
		return s.fail("$", "empty command", "Error: the command is empty.")
	}
	limit := s.Timeout
	if timeout > 0 {
		limit = min(timeout, max(s.Timeout, maxTimeout*time.Second))
	}
	// "Always allow" covers a command where it was allowed: in a directory
	// outside the working directory, or one that may hold secrets, the
	// same command (cat id_rsa) is another matter.
	var notes []string
	var dirReason string
	if dir != "" {
		notes = append(notes, "in "+display.ShortPath(dir))
		dirReason = readReason(resolve(dir))
	}
	if timeout > 0 {
		notes = append(notes, "timeout "+timeoutText(limit))
	}
	if dirReason != "" {
		notes = append(notes, "("+dirReason+")")
	}
	note := strings.Join(notes, " · ")
	edited, titled := false, false
	for {
		if !titled {
			s.ui().Open("$ "+command, note)
			titled = true
		}
		if s.AutoApprove || dirReason == "" && s.allowed[alwaysKey(command)] {
			break
		}
		var always *ui.Item
		if key := display.Safe(alwaysKey(command)); key != "" && dirReason == "" {
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
			v, err := s.ui().Edit("Edit command", shown)
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

	var out tailBuffer
	start := time.Now()
	err := runCommand(ctx, command, dir, limit, io.MultiWriter(&out, s.ui().Output()))
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
		s.ui().Close(true, "exit 0 · "+took)
		return result
	case errors.Is(err, context.Canceled):
		s.ui().Close(false, "cancelled")
		return result + "\n[The user interrupted the command.]"
	case errors.Is(err, errTimeout):
		s.ui().Close(false, "timed out after "+timeoutText(limit))
		return result + fmt.Sprintf("\n[The command timed out after %s. Run it again with a longer timeout if it needs one.]", timeoutText(limit))
	case errors.As(err, &exit):
		s.ui().Close(false, fmt.Sprintf("exit %d · %s", exit.ExitCode(), took))
		return result + fmt.Sprintf("\n[The command exited with code %d.]", exit.ExitCode())
	}
	s.ui().Close(false, err.Error())
	return result + "\n[The command failed: " + err.Error() + "]"
}

func (s *Session) readFile(ctx context.Context, cancel context.CancelFunc, path string, offset, limit int) docsgpt.ToolResult {
	text := func(s string) docsgpt.ToolResult { return docsgpt.ToolResult{Content: s} }
	title := "read " + display.ShortPath(path)
	if offset > 1 || limit > 0 {
		title += fmt.Sprintf(":%d", max(offset, 1))
		if limit > 0 {
			title += fmt.Sprintf("-%d", max(offset, 1)+limit-1)
		}
	}
	real, err := regularFile(path)
	if err != nil {
		return text(s.fail(title, err.Error(), "Error: "+err.Error()))
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
	s.ui().Open(title, note)
	if reason != "" {
		choice, refusal := s.ask(cancel, "read this file once",
			&ui.Item{Label: "Always allow reads", Description: "read any file without asking this session"}, false)
		if refusal != "" {
			return text(refusal)
		}
		s.allowReads = choice == "always"
	}

	readCtx := ctx
	if s.Timeout > 0 {
		var stop context.CancelFunc
		readCtx, stop = context.WithTimeout(ctx, s.Timeout)
		defer stop()
	}
	if img, err := readImage(real); img != nil || err != nil {
		if err != nil {
			s.ui().Close(false, err.Error())
			return text("Error: " + err.Error())
		}
		s.ui().Close(true, img.status)
		return docsgpt.ToolResult{
			Content: fmt.Sprintf("[Image %s: %s. It is attached to this result.]", display.ShortPath(path), img.status),
			Parts:   []docsgpt.ContentPart{docsgpt.ImagePart(img.mimeType, img.data)},
		}
	}
	body, shown, total, err := readFile(readCtx, real, offset, limit)
	switch {
	case ctx.Err() != nil:
		s.ui().Close(false, "cancelled")
		return text("The user interrupted the read.")
	case errors.Is(err, context.DeadlineExceeded):
		s.ui().Close(false, "timed out after "+display.Duration(s.Timeout))
		return text("Error: reading the file timed out.")
	case err != nil:
		s.ui().Close(false, err.Error())
		return text("Error: " + err.Error())
	}
	status := lines(shown)
	if shown < total {
		status += fmt.Sprintf(" of %d", total)
	}
	s.ui().Close(true, status)
	if total == 0 {
		return text("(empty file)")
	}
	return text(body)
}

// timeoutText writes a timeout as 45s, 2m, 1m30s or 1h1m: hours, minutes
// and seconds, leaving out zeros at the end.
func timeoutText(d time.Duration) string {
	sec := int(d.Round(time.Second) / time.Second)
	h, m := sec/3600, sec/60%60
	sec %= 60
	var out string
	if h > 0 {
		out += fmt.Sprintf("%dh", h)
	}
	if m > 0 || h > 0 && sec > 0 {
		out += fmt.Sprintf("%dm", m)
	}
	if sec > 0 || out == "" {
		out += fmt.Sprintf("%ds", sec)
	}
	return out
}

// resolve makes path absolute and resolves its symlinks as far as it
// exists: a file to be created is resolved through its directory.
func resolve(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	rest := ""
	for d := abs; ; {
		if real, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return abs
		}
		rest = filepath.Join(filepath.Base(d), rest)
		d = parent
	}
}

// writeReason says why writing path needs the user's approval even after
// "Always allow writes", or "" when it does not: as for reads (readReason),
// and inside a .git directory, whose hooks and config run code.
func writeReason(path string) string {
	real := resolve(path)
	if reason := readReason(real); reason != "" {
		return reason
	}
	if slices.Contains(strings.Split(real, string(filepath.Separator)), ".git") {
		return "inside a .git directory"
	}
	return ""
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
		return s.fail(title, path+" is not a regular file", "Error: "+path+" is not a regular file")
	}
	var before []byte
	if err == nil && fi.Size() <= maxPreviewBytes {
		before, _ = os.ReadFile(path)
	}
	preview, added, removed := display.DiffPreview(string(before), content, 10)
	reason := s.writeApproval(path)
	var note string
	switch {
	case errors.Is(err, os.ErrNotExist):
		note = "(new, " + lines(added) + ")"
	case err == nil && fi.Size() > maxPreviewBytes:
		preview = nil
		note = fmt.Sprintf("(replaces %d bytes with %s)", fi.Size(), lines(added))
	default:
		note = fmt.Sprintf("(+%d −%d)", added, removed)
	}
	if reason != "" {
		note += " (" + reason + ")"
	}
	s.ui().Open(title, note)
	s.ui().Lines(preview)

	if refusal := s.approveWrite(cancel, "write this file once", reason); refusal != "" {
		return refusal
	}
	if ctx.Err() != nil {
		s.ui().Close(false, "cancelled")
		return "The user interrupted the run before this tool call ran."
	}
	if err := writeFile(path, content); err != nil {
		s.ui().Close(false, err.Error())
		return "Error: " + err.Error()
	}
	s.ui().Close(true, "wrote "+lines(len(splitLines(content))))
	return fmt.Sprintf("Wrote %d bytes to %s.", len(content), path)
}

// maxEditBytes is the largest file edit_file changes.
const maxEditBytes = 10 << 20

func (s *Session) editFile(ctx context.Context, cancel context.CancelFunc, path string, edits []edit) string {
	title := "edit " + display.ShortPath(path)
	real, err := regularFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s.fail(title, "no such file", "Error: "+path+" does not exist; create a new file with write_file.")
	}
	if err != nil {
		return s.fail(title, err.Error(), "Error: "+err.Error())
	}
	before, err := readText(real, maxEditBytes)
	if err != nil {
		return s.fail(title, err.Error(), "Error: "+err.Error())
	}
	after, at, err := applyEdits(before, edits)
	if err != nil {
		return s.fail(title, "not applied", "Error: "+err.Error())
	}
	preview, added, removed := display.DiffPreview(before, after, 10)
	reason := s.writeApproval(real)
	note := fmt.Sprintf("(+%d −%d)", added, removed)
	if reason != "" {
		note += " (" + reason + ")"
	}
	s.ui().Open(title, note)
	s.ui().Lines(preview)

	if refusal := s.approveWrite(cancel, "make this edit once", reason); refusal != "" {
		return refusal
	}
	if ctx.Err() != nil {
		s.ui().Close(false, "cancelled")
		return "The user interrupted the run before this tool call ran."
	}
	// What was approved is a change to the file as it was shown.
	if now, err := readText(real, maxEditBytes); err != nil || now != before {
		s.ui().Close(false, "the file changed")
		return "Error: " + path + " changed while the edit waited for approval; read it again."
	}
	fi, err := os.Stat(real)
	if err == nil {
		err = os.WriteFile(real, []byte(after), fi.Mode().Perm())
	}
	if err != nil {
		s.ui().Close(false, err.Error())
		return "Error: " + err.Error()
	}
	s.ui().Close(true, fmt.Sprintf("+%d −%d", added, removed))
	where := make([]string, len(at))
	for i, n := range at {
		where[i] = fmt.Sprint(n)
	}
	blocks := "1 block"
	if len(at) > 1 {
		blocks = fmt.Sprintf("%d blocks", len(at))
	}
	return fmt.Sprintf("Edited %s: replaced %s, at line %s.", path, blocks, strings.Join(where, ", "))
}

// writeApproval is why a write or edit of path must be asked about even
// with "Always allow writes" (writeReason), or "" when it need not be.
func (s *Session) writeApproval(path string) string {
	if s.AutoApprove {
		return ""
	}
	return writeReason(path)
}

// approveWrite asks about a write or edit, unless AutoApprove is set or
// the user allowed writes and reason is "". "Always allow writes" is
// offered only for a file it would cover: one with no reason to ask.
func (s *Session) approveWrite(cancel context.CancelFunc, once, reason string) (refusal string) {
	if s.AutoApprove || s.allowWrites && reason == "" {
		return ""
	}
	var always *ui.Item
	if reason == "" {
		always = &ui.Item{Label: "Always allow writes", Description: "write and edit files in the working directory without asking this session"}
	}
	choice, refusal := s.ask(cancel, once, always, false)
	if refusal != "" {
		return refusal
	}
	if choice == "always" {
		s.allowWrites = true
	}
	return ""
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
	choice, err := s.ui().Choose(items)
	switch {
	case errors.Is(err, ui.ErrCancelled):
		cancel()
		s.ui().Close(false, "cancelled")
		return "", "The user interrupted the run before this tool call ran."
	case err != nil:
		s.ui().Close(false, "not run: "+err.Error())
		return "", "The tool call could not be approved: " + err.Error()
	case choice == "deny":
		s.ui().Close(false, "denied")
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
