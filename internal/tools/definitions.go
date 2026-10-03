package tools

import (
	"encoding/json"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// ToolDefinitions returns the tool schemas to send in chat completion requests.
func ToolDefinitions() []docsgpt.Tool {
	return []docsgpt.Tool{
		{
			Type: "function",
			Function: docsgpt.ToolFunction{
				Name:        "run_command",
				Description: "Execute a shell command on the user's local machine. The user approves each command before it runs. Use this to help with file operations, git commands, builds, deployments, and system administration tasks. Returns stdout and stderr combined, truncated to the last 2000 lines or 50KB.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"command": {
							"type": "string",
							"description": "The shell command to execute"
						},
						"working_directory": {
							"type": "string",
							"description": "Working directory for the command. Defaults to current directory."
						}
					},
					"required": ["command"]
				}`),
			},
		},
		{
			Type: "function",
			Function: docsgpt.ToolFunction{
				Name:        "read_file",
				Description: "Read a text file on the user's local machine. Files outside the working directory, or that may hold secrets, need the user's approval. Output is truncated to 2000 lines or 50KB; use offset/limit for large files, and continue with offset until complete.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "Path to the file to read (relative to working directory or absolute)"
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
				Name:        "write_file",
				Description: "Write content to a file on the user's local machine, replacing it if it exists and creating missing parent directories. The user approves each write.",
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
