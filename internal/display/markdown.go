package display

import (
	"github.com/charmbracelet/glamour"
)

var mdRenderer *glamour.TermRenderer

// InitMarkdown sets up the terminal markdown renderer.
func InitMarkdown() {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(termWidth()),
	)
	if err != nil {
		return // fallback to raw output
	}
	mdRenderer = r
}

// RenderMarkdown renders a markdown string to styled terminal output.
// Falls back to the raw string if rendering fails.
func RenderMarkdown(md string) string {
	if mdRenderer == nil {
		InitMarkdown()
	}
	if mdRenderer == nil {
		return md
	}
	out, err := mdRenderer.Render(md)
	if err != nil {
		return md
	}
	return out
}
