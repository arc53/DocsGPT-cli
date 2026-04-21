package display

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// abbreviateHome replaces the home directory prefix with ~.
func abbreviateHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// RenderHeader renders a metadata header bar with key, server, and cwd.
func RenderHeader(keyName, baseURL, cwd string) string {
	w := termWidth()

	sep := T.Muted.Render(" │ ")
	parts := []string{
		T.Accent.Bold(true).Render("docsgpt"),
		T.Muted.Render("key: ") + T.Text.Render(keyName),
		T.Muted.Render("server: ") + T.Text.Render(baseURL),
	}

	if cwd != "" {
		short := abbreviateHome(cwd)
		// Only show last 2 path components if long
		if len(short) > 30 {
			short = "~/" + filepath.Base(filepath.Dir(short)) + "/" + filepath.Base(short)
		}
		parts = append(parts, T.Muted.Render("cwd: ")+T.Text.Render(short))
	}

	header := strings.Join(parts, sep)

	style := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(T.Border.GetForeground()).
		Width(w - 2)

	return style.Render(header)
}

// RenderHints renders a hint bar for the given mode.
func RenderHints(mode string) string {
	var hints string
	switch mode {
	case "chat":
		hints = "/quit  /clear  /copy  /think │ Ctrl+C to exit"
	case "ask":
		hints = ""
	default:
		hints = ""
	}

	if hints == "" {
		return ""
	}

	return T.Muted.Render(hints)
}
