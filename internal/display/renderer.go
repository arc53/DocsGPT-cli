package display

import (
	"fmt"
	"strings"

	"docsgpt-cli/internal/api"
)

// StreamRenderer accumulates streaming content and provides markdown rendering on finish.
type StreamRenderer struct {
	contentBuf    strings.Builder
	reasoningBuf  strings.Builder
	ShowReasoning bool
}

// NewStreamRenderer creates a new StreamRenderer.
func NewStreamRenderer() *StreamRenderer {
	return &StreamRenderer{}
}

// Delta processes a streaming delta, printing content immediately.
// Reasoning is printed only if ShowReasoning is true.
func (r *StreamRenderer) Delta(delta api.Delta) {
	if delta.ReasoningContent != "" {
		r.reasoningBuf.WriteString(delta.ReasoningContent)
		if r.ShowReasoning {
			fmt.Print(T.Reasoning.Render(delta.ReasoningContent))
		}
	}
	if delta.Content != "" {
		r.contentBuf.WriteString(delta.Content)
		fmt.Print(delta.Content)
	}
}

// Finish returns the accumulated content rendered as markdown.
// Returns empty string if there was no content.
func (r *StreamRenderer) Finish() string {
	content := r.contentBuf.String()
	if content == "" {
		return ""
	}
	// Only render markdown if content contains markdown syntax
	if containsMarkdown(content) {
		return RenderMarkdown(content)
	}
	return ""
}

// Content returns the raw accumulated content.
func (r *StreamRenderer) Content() string {
	return r.contentBuf.String()
}

// containsMarkdown checks if text likely contains markdown formatting.
func containsMarkdown(s string) bool {
	indicators := []string{"```", "## ", "### ", "* ", "- ", "1. ", "**", "> "}
	for _, ind := range indicators {
		if strings.Contains(s, ind) {
			return true
		}
	}
	return false
}

// StreamDelta prints a streaming delta to the terminal (legacy convenience function).
func StreamDelta(delta api.Delta) {
	if delta.ReasoningContent != "" {
		fmt.Print(T.Reasoning.Render(delta.ReasoningContent))
	}
	if delta.Content != "" {
		fmt.Print(delta.Content)
	}
}
