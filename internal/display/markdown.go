package display

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// newMarkdown returns a markdown renderer for the active theme that wraps at
// width and leaves no document margin. Nil when glamour refuses the options.
func newMarkdown(width int) *glamour.TermRenderer {
	style := styles.LightStyleConfig
	switch {
	case colorless():
		style = styles.NoTTYStyleConfig
	case darkBackground:
		style = styles.DarkStyleConfig
	}
	margin := uint(0)
	style.Document.Margin = &margin

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithColorProfile(termenv.ColorProfile()),
	)
	if err != nil {
		return nil
	}
	return r
}

// renderMarkdown renders md with r (raw md when r is nil or fails) and tidies
// glamour's output: no padding at the end of a line, no blank lines around.
func renderMarkdown(r *glamour.TermRenderer, md string) string {
	out := md
	if r != nil {
		if s, err := r.Render(md); err == nil {
			out = s
		}
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		lines[i] = trimPadding(line)
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

var trailingPadding = regexp.MustCompile(`(?:\x1b\[[0-9;]*m| )+$`)

// trimPadding drops the spaces glamour pads every line with to the full
// width (styled ones included), keeping a reset if styling was cut off.
func trimPadding(line string) string {
	line = trailingPadding.ReplaceAllStringFunc(line, func(tail string) string {
		if strings.Contains(tail, "\x1b") {
			return "\x1b[0m"
		}
		return ""
	})
	if lipgloss.Width(line) == 0 {
		return ""
	}
	return line
}
