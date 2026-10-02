package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// Session runs the model's tool calls for one chat session or ask run.
// Approval is the only gate: every command and write is shown and asked
// about, unless AutoApprove is set or the user chose "Always allow" for it
// earlier in the session. Reads never ask.
type Session struct {
	AutoApprove bool
	Timeout     time.Duration // per command

	allowWrites bool
	allowed     map[string]bool // first words of always-allowed commands
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
		return s.readFile(args.Path, args.Offset, args.Limit)
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
		items := []ui.Item{{Label: "Approve", Value: "approve", Keys: []string{"a", "y"}}}
		if key := alwaysKey(command); key != "" {
			items = append(items, ui.Item{Label: "Always allow " + key, Value: "always", Keys: []string{"l"}})
		}
		items = append(items,
			ui.Item{Label: "Deny", Value: "deny", Keys: []string{"d", "n"}},
			ui.Item{Label: "Edit", Value: "edit", Keys: []string{"e"}})
		choice, refusal := s.ask(cancel, items)
		if refusal != "" {
			return refusal
		}
		if choice == "edit" {
			v, err := ui.Input{Title: "Edit command", Value: command, Stderr: true, Summary: func(string) string { return "" }}.Run()
			if v = strings.TrimSpace(v); err == nil && v != "" && v != command {
				command, edited, titled = v, true, false
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

func (s *Session) readFile(path string, offset, limit int) string {
	title := "read " + display.ShortPath(path)
	if offset > 1 || limit > 0 {
		title += fmt.Sprintf(":%d", max(offset, 1))
		if limit > 0 {
			title += fmt.Sprintf("-%d", max(offset, 1)+limit-1)
		}
	}
	display.ToolTitle(title, "")
	text, shown, total, err := readFile(path, offset, limit)
	if err != nil {
		display.ToolStatus(false, err.Error())
		return "Error: " + err.Error()
	}
	status := fmt.Sprintf("%d lines", shown)
	if shown < total {
		status += fmt.Sprintf(" of %d", total)
	}
	display.ToolStatus(true, status)
	return text
}

func (s *Session) writeFile(ctx context.Context, cancel context.CancelFunc, path, content string) string {
	before, err := os.ReadFile(path)
	isNew := errors.Is(err, os.ErrNotExist)
	preview, added, removed := display.DiffPreview(string(before), content, 10)
	if isNew {
		display.ToolTitle("write "+display.ShortPath(path), fmt.Sprintf("(new, %d lines)", added))
	} else {
		display.ToolTitle("write "+display.ShortPath(path), fmt.Sprintf("(+%d −%d)", added, removed))
	}
	display.ToolLines(preview)

	if !s.AutoApprove && !s.allowWrites {
		choice, refusal := s.ask(cancel, []ui.Item{
			{Label: "Approve", Value: "approve", Keys: []string{"a", "y"}},
			{Label: "Always allow writes", Value: "always", Keys: []string{"l"}},
			{Label: "Deny", Value: "deny", Keys: []string{"d", "n"}},
		})
		if refusal != "" {
			return refusal
		}
		s.allowWrites = choice == "always"
	}
	if ctx.Err() != nil {
		return "The user interrupted the run before this tool call ran."
	}
	if err := writeFile(path, content); err != nil {
		display.ToolStatus(false, err.Error())
		return "Error: " + err.Error()
	}
	display.ToolStatus(true, fmt.Sprintf("wrote %d lines", len(splitLines(content))))
	return fmt.Sprintf("Wrote %d bytes to %s.", len(content), path)
}

// ask shows the approval choices under the tool's title. It returns the
// choice, or the result for the model when the call must not run: denied,
// or cancelled (which also cancels the run).
func (s *Session) ask(cancel context.CancelFunc, items []ui.Item) (choice, refusal string) {
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
	}
	return choice, ""
}

// alwaysKey returns what "Always allow" covers for command: its first
// word, for a simple command only. Shell operators, substitutions,
// redirections or several lines always ask, and so do shells and wrappers
// that run arbitrary commands themselves. "" when it cannot be allowed.
func alwaysKey(command string) string {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, ";&|<>`$()\n\r\\") {
		return ""
	}
	first := strings.Fields(command)[0]
	switch first {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "env", "sudo", "doas", "su", "xargs", "eval",
		"exec", "nohup", "time", "timeout", "nice", "command", "builtin", "watch", "ssh", "find":
		return ""
	}
	return first
}
