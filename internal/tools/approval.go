package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
)

type ApprovalResult int

const (
	Approved ApprovalResult = iota
	Denied
	Edited
)

// RequestApproval displays a tool call and asks the user to approve, deny, or edit it.
// Returns the result and potentially edited arguments.
func RequestApproval(toolName string, rawArgs string) (ApprovalResult, string, error) {
	yellow := color.New(color.FgYellow).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()

	fmt.Println()
	fmt.Printf("%s Agent wants to use tool: %s\n", yellow("🔧"), cyan(toolName))

	switch toolName {
	case "run_command":
		var args struct {
			Command          string `json:"command"`
			WorkingDirectory string `json:"working_directory"`
		}
		json.Unmarshal([]byte(rawArgs), &args)
		fmt.Printf("   $ %s\n", cyan(args.Command))
		if args.WorkingDirectory != "" {
			fmt.Printf("   in: %s\n", args.WorkingDirectory)
		}

	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal([]byte(rawArgs), &args)
		fmt.Printf("   Read: %s\n", cyan(args.Path))

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		json.Unmarshal([]byte(rawArgs), &args)
		fmt.Printf("   Write to: %s\n", cyan(args.Path))
		// Show a preview of content (first 5 lines)
		lines := strings.Split(args.Content, "\n")
		preview := lines
		if len(preview) > 5 {
			preview = preview[:5]
		}
		for _, line := range preview {
			fmt.Printf("   | %s\n", line)
		}
		if len(lines) > 5 {
			fmt.Printf("   ... (%d more lines)\n", len(lines)-5)
		}

	default:
		fmt.Printf("   Arguments: %s\n", rawArgs)
	}

	fmt.Println()
	fmt.Print("   [A]pprove  [D]eny  [E]dit > ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return Denied, rawArgs, err
	}
	input = strings.TrimSpace(strings.ToLower(input))

	switch input {
	case "a", "approve", "y", "yes", "":
		return Approved, rawArgs, nil
	case "d", "deny", "n", "no":
		return Denied, rawArgs, nil
	case "e", "edit":
		return editArgs(toolName, rawArgs)
	default:
		fmt.Println("   Invalid choice, denying.")
		return Denied, rawArgs, nil
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

		fmt.Printf("   Edit command (current: %s)\n", args.Command)
		fmt.Print("   $ ")
		newCmd, err := reader.ReadString('\n')
		if err != nil {
			return Denied, rawArgs, err
		}
		args.Command = strings.TrimSpace(newCmd)
		edited, _ := json.Marshal(args)
		return Edited, string(edited), nil
	}

	// For other tools, let user edit raw JSON
	fmt.Printf("   Edit arguments JSON (current: %s)\n", rawArgs)
	fmt.Print("   > ")
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
