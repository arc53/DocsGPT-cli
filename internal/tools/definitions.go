package tools

import (
	"encoding/json"

	"docsgpt-cli/internal/api"
)

// ToolDefinitions returns the tool schemas to send in chat completion requests.
func ToolDefinitions() []api.Tool {
	return []api.Tool{
		{
			Type: "function",
			Function: api.ToolFunction{
				Name:        "run_command",
				Description: "Execute a shell command on the user's local machine. The user will be prompted to approve before execution. Use this to help with file operations, git commands, builds, deployments, and system administration tasks.",
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
			Function: api.ToolFunction{
				Name:        "read_file",
				Description: "Read the contents of a file on the user's local machine.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "Path to the file to read (relative to working directory or absolute)"
						}
					},
					"required": ["path"]
				}`),
			},
		},
		{
			Type: "function",
			Function: api.ToolFunction{
				Name:        "write_file",
				Description: "Write content to a file on the user's local machine. The user will be prompted to approve.",
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
