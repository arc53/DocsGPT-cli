package display

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// ChatWelcome is the line that opens a chat: name, version and key hints.
func ChatWelcome(version string) string {
	line := T.Accent.Bold(true).Render("docsgpt")
	if version != "" {
		line += T.Dim.Render(" " + version)
	}
	pairs := []string{"/", "commands", "!", "run a command", "ctrl+j", "new line", "ctrl+c twice", "quit"}
	for i := 0; i < len(pairs); i += 2 {
		next := line + T.Dim.Render(" · "+pairs[i]+" ") + T.Muted.Render(pairs[i+1])
		if lipgloss.Width(next) > termWidth() {
			break
		}
		line = next
	}
	return line
}

// ChatFooter is the plain text of the line under the chat input: the
// working directory, the key and the server.
func ChatFooter(cwd, keyName, baseURL string) string {
	host := strings.TrimSuffix(baseURL, "/")
	for _, scheme := range []string{"https://", "http://"} {
		host = strings.TrimPrefix(host, scheme)
	}
	tail := " · " + keyName + " · " + host
	return shortenPath(abbreviateHome(cwd), max(8, termWidth()/2-lipgloss.Width(tail))) + tail
}

// UserMessage prints what the user sent as a block on a subtle background
// (a "❯" prefix without colors), followed by a blank line.
func UserMessage(text string) {
	width := termWidth()
	text = strings.ReplaceAll(strings.TrimRight(text, "\n"), "\t", "    ")
	if Colorless() {
		lines := strings.Split(ansi.Wrap(text, max(width-2, 10), ""), "\n")
		for i, l := range lines {
			if i == 0 {
				lines[i] = T.ToolTitle.Render("❯") + " " + l
			} else {
				lines[i] = "  " + l
			}
		}
		fmt.Println(strings.Join(lines, "\n") + "\n")
		return
	}
	fmt.Println(lipgloss.NewStyle().Background(colUserBg).Foreground(colText).Width(width).Padding(1, 1).Render(text) + "\n")
}

// Ago renders how long ago t was: "just now", "5m ago", "3h ago", "2d ago",
// or the date after a month.
func Ago(t time.Time) string {
	switch d := time.Since(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Local().Format("Jan 2, 2006")
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
