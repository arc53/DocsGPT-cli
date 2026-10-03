package tools

import (
	"encoding/json"
	"fmt"
	"runtime"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// maxTimeout caps the timeout a model may ask a command for, unless the
// user's own (--tool-timeout) is longer.
const maxTimeout = 3600

// ToolDefinitions returns the tool schemas to send in chat completion
// requests. timeout is the default a command gets, in seconds.
func ToolDefinitions(timeout int) []docsgpt.Tool {
	syntax := "POSIX sh"
	if runtime.GOOS == "windows" {
		syntax = "cmd.exe (not bash)"
	}
	return []docsgpt.Tool{
		{
			Type: "function",
			Function: docsgpt.ToolFunction{
				Name: "run_command",
				Description: fmt.Sprintf("Execute a shell command on the user's local machine (%s) with %s syntax. "+
					"The user approves each command before it runs. Use it for git, builds, tests, searching (rg, git grep, git ls-files) and other system tasks; "+
					"use read_file, edit_file and write_file for files instead of cat, sed or echo. "+
					"The command has no terminal: interactive prompts fail at once, so pass non-interactive flags (--yes, --no-pager, --no-edit). "+
					"Returns stdout and stderr combined, truncated to the last 2000 lines or 50KB, and the exit code when it is not 0.",
					runtime.GOOS, syntax),
				Parameters: json.RawMessage(fmt.Sprintf(`{
					"type": "object",
					"properties": {
						"command": {
							"type": "string",
							"description": "The shell command to execute"
						},
						"working_directory": {
							"type": "string",
							"description": "Working directory for the command. Defaults to the current directory."
						},
						"timeout": {
							"type": "integer",
							"description": "Seconds the command may run before it is stopped. Defaults to %d; raise it for installs, builds and test suites (at most %d)."
						}
					},
					"required": ["command"]
				}`, timeout, max(timeout, maxTimeout))),
			},
		},
		{
			Type: "function",
			Function: docsgpt.ToolFunction{
				Name: "read_file",
				Description: "Read a file on the user's local machine: a text file, or an image (PNG, JPEG, GIF, WebP, BMP), which is shown to you. " +
					"Files outside the working directory, or that may hold secrets, need the user's approval. " +
					"Text is truncated to 2000 lines or 50KB; use offset/limit for large files, and continue with offset until complete. " +
					"Read a file before you edit it.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "Path to the file to read (relative to the working directory, absolute, or starting with ~)"
						},
						"offset": {
							"type": "integer",
							"description": "Line number to start reading from (1-indexed)"
						},
						"limit": {
							"type": "integer",
							"description": "Maximum number of lines to read"
						}
					},
					"required": ["path"]
				}`),
			},
		},
		{
			Type: "function",
			Function: docsgpt.ToolFunction{
				Name: "edit_file",
				Description: "Edit a file on the user's local machine by exact text replacement. " +
					"Every edits[].old_text must match exactly one place in the file as it is now, whitespace and line breaks included, and must not overlap another edit; " +
					"all edits of a call are matched against the file before any of them applies. " +
					"Make several changes to one file with one call and several edits. Keep old_text as short as it can be while unique; " +
					"don't pad it with unchanged lines to join distant changes. The user approves each edit, shown as a diff.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "Path to the file to edit"
						},
						"edits": {
							"type": "array",
							"description": "The replacements, each matched against the original file",
							"items": {
								"type": "object",
								"properties": {
									"old_text": {
										"type": "string",
										"description": "Text to replace: exactly as in the file, unique in it, not overlapping another edit's old_text"
									},
									"new_text": {
										"type": "string",
										"description": "Text to put in its place"
									}
								},
								"required": ["old_text", "new_text"]
							},
							"minItems": 1
						}
					},
					"required": ["path", "edits"]
				}`),
			},
		},
		{
			Type: "function",
			Function: docsgpt.ToolFunction{
				Name: "write_file",
				Description: "Write content to a file on the user's local machine, replacing it if it exists and creating missing parent directories. " +
					"Use it for new files and complete rewrites; change part of a file with edit_file. The user approves each write.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "Path to the file to write"
						},
						"content": {
							"type": "string",
							"description": "Content to write to the file"
						}
					},
					"required": ["path", "content"]
				}`),
			},
		},
	}
}
