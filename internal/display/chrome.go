package display

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// RenderHeader renders the one dim line that opens ask and chat:
// "docsgpt [version] · key · server · cwd". The cwd loses leading
// directories so the line never wraps.
func RenderHeader(version, keyName, baseURL, cwd string) string {
	head := T.Accent.Bold(true).Render("docsgpt")
	if version != "" {
		head += T.Dim.Render(" " + version)
	}
	sep := T.Dim.Render(" · ")
	host := strings.TrimSuffix(baseURL, "/")
	for _, scheme := range []string{"https://", "http://"} {
		host = strings.TrimPrefix(host, scheme)
	}
	line := head
	for _, part := range []string{keyName, host} {
		if part != "" {
			line += sep + T.Dim.Render(part)
		}
	}
	width := termWidth()
	if cwd != "" {
		if room := width - lipgloss.Width(line+sep); room >= 8 {
			line += sep + T.Dim.Render(shortenPath(abbreviateHome(cwd), room))
		}
	}
	return ansi.Truncate(line, width, "…")
}

// RenderHints renders chat's key hints: keys dim, actions muted.
func RenderHints() string {
	pairs := []string{"/", "commands", "ctrl+c", "cancel", "ctrl+d", "quit"}
	var parts []string
	for i := 0; i < len(pairs); i += 2 {
		parts = append(parts, T.Dim.Render(pairs[i])+" "+T.Muted.Render(pairs[i+1]))
	}
	return strings.Join(parts, T.Dim.Render(" · "))
}

// abbreviateHome replaces the home directory prefix with ~.
func abbreviateHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home || strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

// ShortPath shows path relative to the working directory when it lies
// inside it, else with the home directory as ~.
func ShortPath(path string) string {
	if cwd, err := os.Getwd(); err == nil && filepath.IsAbs(path) {
		if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return abbreviateHome(path)
}

// shortenPath drops leading directories of path until it fits in width
// columns, marking the cut with "…"; the last element is kept whole.
func shortenPath(path string, width int) string {
	if lipgloss.Width(path) <= width {
		return path
	}
	sep := string(filepath.Separator)
	parts := strings.Split(path, sep)
	for i := 1; i < len(parts); i++ {
		if short := "…" + sep + strings.Join(parts[i:], sep); lipgloss.Width(short) <= width || i == len(parts)-1 {
			return ansi.Truncate(short, width, "…")
		}
	}
	return ansi.Truncate(path, width, "…")
}
