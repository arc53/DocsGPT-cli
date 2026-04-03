package display

import (
	"fmt"

	"docsgpt-cli/internal/api"

	"github.com/fatih/color"
)

var dimStyle = color.New(color.Faint)

// StreamDelta prints a streaming delta to the terminal.
// Reasoning content is displayed in dim/gray, regular content in normal style.
func StreamDelta(delta api.Delta) {
	if delta.ReasoningContent != "" {
		dimStyle.Print(delta.ReasoningContent)
	}
	if delta.Content != "" {
		fmt.Print(delta.Content)
	}
}
