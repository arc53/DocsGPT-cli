package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"docsgpt-cli/internal/display"
)

type ApprovalResult int

const (
	Approved ApprovalResult = iota
	Denied
	Edited
)

// RequestApproval displays a tool call approval card and asks the user to approve, deny, or edit.
// Returns the result and potentially edited arguments.
func RequestApproval(toolName string, rawArgs string) (ApprovalResult, string, error) {
	detail, preview := extractToolDetail(toolName, rawArgs)
	risk := display.ToolRisk(toolName)

	card := display.RenderApprovalCard(toolName, detail, preview, risk)
	fmt.Println()
	fmt.Println(card)
	fmt.Print("  > ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return Denied, rawArgs, err
	}
	input = strings.TrimSpace(strings.ToLower(input))

	switch input {
	case "1", "a", "approve", "y", "yes", "":
		return Approved, rawArgs, nil
	case "2", "d", "deny", "n", "no":
		return Denied, rawArgs, nil
	case "3", "e", "edit":
		return editArgs(toolName, rawArgs)
	default:
		fmt.Println(display.Muted("  Invalid choice, denying."))
		return Denied, rawArgs, nil
	}
}

// extractToolDetail returns a detail string and optional preview lines for the tool.
func extractToolDetail(toolName string, rawArgs string) (string, []string) {
	switch toolName {
	case "run_command":
		var args struct {
			Command          string `json:"command"`
			WorkingDirectory string `json:"working_directory"`
		}
		json.Unmarshal([]byte(rawArgs), &args)
		detail := "$ " + args.Command
		if args.WorkingDirectory != "" {
			detail += "\nin: " + args.WorkingDirectory
		}
		return detail, nil

	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal([]byte(rawArgs), &args)
		return "Read: " + args.Path, nil

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		json.Unmarshal([]byte(rawArgs), &args)
		lines := strings.Split(args.Content, "\n")
		preview := lines
		if len(preview) > 5 {
			preview = preview[:5]
			preview = append(preview, fmt.Sprintf("... (%d more lines)", len(lines)-5))
		}
		return "Write to: " + args.Path, preview

	default:
		return "Arguments: " + rawArgs, nil
	}
}

func editArgs(toolName string, rawArgs string) (ApprovalResult, string, error) {
	reader := bufio.NewReader(os.Stdin)

	if toolName == "run_command" {
		var args struct {
			Command          string `json:"command"`
			WorkingDirectory string `json:"working_directory"`
		}
		json.Unmarshal([]byte(rawArgs), &args)

		fmt.Printf("  Edit command (current: %s)\n", args.Command)
		fmt.Print("  $ ")
		newCmd, err := reader.ReadString('\n')
		if err != nil {
			return Denied, rawArgs, err
		}
		args.Command = strings.TrimSpace(newCmd)
		edited, _ := json.Marshal(args)
		return Edited, string(edited), nil
	}

	// For other tools, let user edit raw JSON
	fmt.Printf("  Edit arguments JSON (current: %s)\n", rawArgs)
	fmt.Print("  > ")
	newArgs, err := reader.ReadString('\n')
	if err != nil {
		return Denied, rawArgs, err
	}
	newArgs = strings.TrimSpace(newArgs)
	if newArgs == "" {
		return Denied, rawArgs, nil
	}
	return Edited, newArgs, nil
}
