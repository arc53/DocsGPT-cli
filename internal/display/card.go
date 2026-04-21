package display

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

// termWidth returns the current terminal width, defaulting to 80.
func termWidth() int {
	w, _, err := term.GetSize(0) // stdin fd
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// cardWidth returns a clamped width for card rendering.
func cardWidth() int {
	w := termWidth()
	if w > 72 {
		return 72
	}
	if w < 40 {
		return w - 2 // minimal padding
	}
	return w - 4
}

// RenderApprovalCard builds a bordered approval card for a tool call.
func RenderApprovalCard(toolName, detail string, preview []string, risk string) string {
	w := cardWidth()

	// Risk badge
	var badge string
	switch risk {
	case "safe":
		badge = T.Success.Render(" SAFE ")
	case "caution":
		badge = T.Warn.Render(" CAUTION ")
	case "danger":
		badge = T.Danger.Render(" DANGER ")
	default:
		badge = T.Muted.Render(" " + risk + " ")
	}

	// Header line
	header := fmt.Sprintf("🔧 %s  %s", T.Accent.Bold(true).Render(toolName), badge)

	// Detail line
	detailLine := T.Info.Render(detail)

	// Build body parts
	parts := []string{header, detailLine}

	// Preview block
	if len(preview) > 0 {
		previewStyle := lipgloss.NewStyle().
			Foreground(T.Muted.GetForeground()).
			PaddingLeft(1)
		var previewLines []string
		for _, line := range preview {
			previewLines = append(previewLines, "│ "+line)
		}
		parts = append(parts, previewStyle.Render(strings.Join(previewLines, "\n")))
	}

	// Separator + choices
	choices := fmt.Sprintf("  %s  %s  %s",
		T.Selection.Render("[1] Approve"),
		T.Muted.Render("[2] Deny"),
		T.Muted.Render("[3] Edit"),
	)
	parts = append(parts, "", choices)

	body := strings.Join(parts, "\n")

	// Wrap in bordered box
	useRounded := termWidth() >= 40
	border := lipgloss.NormalBorder()
	if useRounded {
		border = lipgloss.RoundedBorder()
	}

	cardStyle := lipgloss.NewStyle().
		Border(border).
		BorderForeground(T.Border.GetForeground()).
		Padding(0, 1).
		Width(w)

	return cardStyle.Render(body)
}

// ToolRisk returns the risk level for a given tool name.
func ToolRisk(toolName string) string {
	switch toolName {
	case "read_file":
		return "safe"
	case "run_command":
		return "caution"
	case "write_file":
		return "caution"
	default:
		return "caution"
	}
}
