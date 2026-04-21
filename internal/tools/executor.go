package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"time"

	"docsgpt-cli/internal/display"
)

// stripToolSuffix removes server-appended suffixes like "_ct0" from tool names.
// The DocsGPT server renames tools (e.g., "write_file" → "write_file_ct0").
var toolSuffixRe = regexp.MustCompile(`_ct\d+$`)

func NormalizeName(name string) string {
	return toolSuffixRe.ReplaceAllString(name, "")
}

type ToolResult struct {
	Output string
	Error  string
}

func (r ToolResult) String() string {
	if r.Error != "" {
		return fmt.Sprintf("Error: %s\nOutput: %s", r.Error, r.Output)
	}
	return r.Output
}

// Execute runs a tool by name with the given arguments JSON and timeout.
// Tool names are normalized to strip server-appended suffixes (e.g., "_ct0").
func Execute(name string, rawArgs string, timeout time.Duration) ToolResult {
	switch NormalizeName(name) {
	case "run_command":
		return executeRunCommand(rawArgs, timeout)
	case "read_file":
		return executeReadFile(rawArgs)
	case "write_file":
		return executeWriteFile(rawArgs)
	default:
		return ToolResult{Error: fmt.Sprintf("unknown tool: %s", name)}
	}
}

func executeRunCommand(rawArgs string, timeout time.Duration) ToolResult {
	var args struct {
		Command          string `json:"command"`
		WorkingDirectory string `json:"working_directory"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ToolResult{Error: "failed to parse arguments: " + err.Error()}
	}

	// Safety check
	safe, reason := IsSafe(args.Command)
	if !safe {
		return ToolResult{Error: fmt.Sprintf("command blocked: %s", reason)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", args.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", args.Command)
	}

	if args.WorkingDirectory != "" {
		cmd.Dir = args.WorkingDirectory
	}

	// Stream output to terminal while capturing
	output, err := cmd.CombinedOutput()
	outStr := TruncateOutput(string(output), maxOutputBytes)

	// Print output in real-time style
	if len(output) > 0 {
		fmt.Print(display.Muted(string(output)))
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return ToolResult{Output: outStr, Error: "command timed out"}
		}
		return ToolResult{Output: outStr, Error: err.Error()}
	}

	return ToolResult{Output: outStr}
}

func executeReadFile(rawArgs string) ToolResult {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ToolResult{Error: "failed to parse arguments: " + err.Error()}
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return ToolResult{Error: err.Error()}
	}

	return ToolResult{Output: TruncateOutput(string(data), maxOutputBytes)}
}

func executeWriteFile(rawArgs string) ToolResult {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ToolResult{Error: "failed to parse arguments: " + err.Error()}
	}

	if err := os.WriteFile(args.Path, []byte(args.Content), 0644); err != nil {
		return ToolResult{Error: err.Error()}
	}

	return ToolResult{Output: fmt.Sprintf("Successfully wrote %d bytes to %s", len(args.Content), args.Path)}
}
