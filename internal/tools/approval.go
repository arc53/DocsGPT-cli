package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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

	input, err := readLine(bufio.NewReader(os.Stdin))
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
		newCmd, err := readLine(reader)
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
	newArgs, err := readLine(reader)
	if err != nil {
		return Denied, rawArgs, err
	}
	newArgs = strings.TrimSpace(newArgs)
	if newArgs == "" {
		return Denied, rawArgs, nil
	}
	return Edited, newArgs, nil
}

// readLine reads one line of user input, terminated by LF or CR. Accepting a
// bare CR keeps the prompt usable even if the terminal is left in raw mode
// (where Enter arrives as '\r' and ReadString('\n') would block forever).
// A trailing LF after a CR is consumed so CRLF counts as one line ending. An
// EOF that follows some input still returns that input.
func readLine(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		c, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && b.Len() > 0 {
				return b.String(), nil
			}
			return b.String(), err
		}
		switch c {
		case '\n':
			return b.String(), nil
		case '\r':
			if next, err := r.Peek(1); err == nil && next[0] == '\n' {
				r.ReadByte()
			}
			return b.String(), nil
		default:
			b.WriteByte(c)
		}
	}
}
