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

// Background says where background processes go: nowhere, a chat (they
// run until it ends, and their events start a turn), or one answer (ask:
// they stop when it ends).
type Background int

const (
	NoBackground Background = iota
	ChatBackground
	AnswerBackground
)

// ToolDefinitions returns the tool schemas to send in chat completion
// requests. timeout is the default a command gets, in seconds.
func ToolDefinitions(timeout int, bg Background) []docsgpt.Tool {
	syntax := "POSIX sh"
	if runtime.GOOS == "windows" {
		syntax = "cmd.exe (not bash)"
	}
	defs := []docsgpt.Tool{
		{
			Type: "function",
			Function: docsgpt.ToolFunction{
				Name: "run_command",
				Description: fmt.Sprintf("Execute a shell command on the user's local machine (%s) with %s syntax. "+
					"The user approves each command before it runs. Use it for git, builds, tests, searching (rg, git grep, git ls-files) and other system tasks; "+
					"use read_file, edit_file and write_file for files instead of cat, sed or echo. "+
					"The command has no terminal: interactive prompts fail at once, so pass non-interactive flags (--yes, --no-pager, --no-edit). "+
					"Returns stdout and stderr combined, truncated to the last 2000 lines or 50KB, and the exit code when it is not 0."+
					backgroundNote(bg),
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
						}%s
					},
					"required": ["command"]
				}`, timeout, max(timeout, maxTimeout), backgroundParam(bg))),
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
	if bg != NoBackground {
		defs = append(defs, processTools(bg, syntax)...)
	}
	return defs
}

// backgroundNote is what run_command's description says about
// run_in_background.
func backgroundNote(bg Background) string {
	switch bg {
	case ChatBackground:
		return " With run_in_background it returns at once with an id (p1) and keeps running on this computer, in this docsgpt-cli session (not on the DocsGPT server; check_job does not know it): " +
			"for dev servers, watchers, long builds and test runs. You are told when it exits; don't sleep or poll meanwhile. " +
			"Don't use &, nohup or setsid for this (they are refused): run_in_background keeps the output and stops it with the chat."
	case AnswerBackground:
		return " With run_in_background it returns at once with an id (p1) and keeps running on this computer (not on the DocsGPT server; check_job does not know it) until your answer ends, when it is stopped: " +
			"for a dev server to test against, say. You are told when it exits with the results of your later tool calls. " +
			"Don't use &, nohup or setsid (they are refused)."
	}
	return ""
}

func backgroundParam(bg Background) string {
	if bg == NoBackground {
		return ""
	}
	return `,
						"run_in_background": {
							"type": "boolean",
							"description": "Start it and return at once with its id instead of waiting for it to end. No timeout applies unless you give one. Read its output with local_output, stop it with local_stop."
						}`
}

// processTools are local_monitor, local_output and local_stop.
func processTools(bg Background, syntax string) []docsgpt.Tool {
	where := "This runs on the user's computer, in this docsgpt-cli session, not on the DocsGPT server: it is not monitor_create, check_job and schedule_task don't know it, and it is stopped when the chat ends."
	told := "You are told of each event as a <local_process_event>, in a new turn when you are idle, so end your turn rather than wait."
	if bg == AnswerBackground {
		where = "This runs on the user's computer, not on the DocsGPT server: it is not monitor_create, check_job and schedule_task don't know it, and it is stopped when your answer ends."
		told = "You are told of its events as a <local_process_event> with the results of your later tool calls."
	}
	return []docsgpt.Tool{
		{
			Type: "function",
			Function: docsgpt.ToolFunction{
				Name: "local_monitor",
				Description: fmt.Sprintf("Start a monitor on the user's computer: a shell command (%s syntax) whose every stdout line is an event; lines within 200ms come as one. "+
					"Use it to be told each time something happens: tail -f app.log | grep --line-buffered ERROR, or a loop that polls and prints a line on each change. "+
					"For one notice when something is done, use run_command with run_in_background and a command that ends then (until curl -sf localhost:3000; do sleep 1; done). "+
					"Print only the lines you would act on, failures included (silence is not success): a monitor that prints too much is stopped. "+
					"Every stage of a pipe must flush each line (grep --line-buffered); stderr goes to its log only. "+
					"It stops when the command exits, after timeout seconds, or with local_stop. The user approves the command. %s %s", syntax, told, where),
				Parameters: json.RawMessage(fmt.Sprintf(`{
					"type": "object",
					"properties": {
						"command": {
							"type": "string",
							"description": "The shell command; each line it prints on stdout is an event"
						},
						"description": {
							"type": "string",
							"description": "What it watches, shown with every event: \"errors in dev.log\", not \"watching logs\""
						},
						"timeout": {
							"type": "integer",
							"description": "Seconds before the monitor is stopped. Defaults to %d, at most %d; start it again if you still need it."
						},
						"working_directory": {
							"type": "string",
							"description": "Working directory for the command. Defaults to the current directory."
						}
					},
					"required": ["command", "description"]
				}`, int(monitorDefault.Seconds()), int(monitorMax.Seconds()))),
			},
		},
		{
			Type: "function",
			Function: docsgpt.ToolFunction{
				Name: "local_output",
				Description: "Show a background process or monitor on the user's computer (run_command with run_in_background, local_monitor): its status and the last lines of its output. " +
					"Without an id, list them all. It is a look at this moment, not a way to wait: don't call it in a loop, you are told when a process exits. " +
					"Not for server jobs (check_job).",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"id": {
							"type": "string",
							"description": "The process id (p1); leave it out to list every process"
						},
						"tail_lines": {
							"type": "integer",
							"description": "How many of the last lines of its output to show. Defaults to 100, at most 2000."
						}
					}
				}`),
			},
		},
		{
			Type: "function",
			Function: docsgpt.ToolFunction{
				Name:        "local_stop",
				Description: "Stop a background process or monitor on the user's computer (p1), with everything it started. Not for server jobs (check_job) or monitors (monitor_cancel).",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"id": {
							"type": "string",
							"description": "The process id (p1)"
						}
					},
					"required": ["id"]
				}`),
			},
		},
	}
}
