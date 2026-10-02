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

// ClaimScreen scrolls what the window shows into the scrollback and puts
// the cursor in its top-left corner, so a chat starts on a clean window
// without erasing anything: newlines from the cursor row r scroll exactly
// the r-1 rows above it away.
func ClaimScreen() {
	_, h := termSize()
	fmt.Print("\r" + strings.Repeat("\n", h-1) + "\x1b[H")
}

// ChatHeader is what opens a chat at the top of the window: a small mark
// beside the name and version, then the key hints (newline names the key
// for a new line), and the instruction files the context sends.
func ChatHeader(version, newline string, files []string) string {
	width := termWidth()
	mark := []string{T.Accent.Render("█▀▄"), T.Accent.Render("█▄▀")}
	name := T.Accent.Bold(true).Render("docsgpt")
	if version != "" {
		name += T.Dim.Render(" " + version)
	}
	keys := ""
	pairs := []string{"/", "commands", "!", "run a command", newline, "new line", "ctrl+c twice", "quit"}
	for i := 0; i < len(pairs); i += 2 {
		next := T.Dim.Render(pairs[i]+" ") + T.Muted.Render(pairs[i+1])
		if keys != "" {
			next = keys + T.Dim.Render(" · ") + next
		}
		if lipgloss.Width(next) > width-5 {
			break
		}
		keys = next
	}
	out := mark[0] + "  " + name + "\n" + mark[1] + "  " + keys + "\n"
	if len(files) > 0 {
		out += "\n" + ansi.Truncate(T.Muted.Render("Context ")+T.Dim.Render(Safe(strings.Join(files, ", "))), width, "…") + "\n"
	}
	return out
}

// ChatFooter is the left of the line under the chat input: the working
// directory and its git branch, fitted beside right.
func ChatFooter(cwd, right string) string {
	branch := ""
	if b := gitBranch(cwd); b != "" {
		branch = " (" + Safe(b) + ")"
	}
	room := termWidth() - lipgloss.Width(right) - 2 - lipgloss.Width(branch)
	return shortenPath(abbreviateHome(cwd), max(8, room)) + branch
}

// gitBranch returns the branch checked out in the repository holding dir
// (a worktree's own), a short commit id on a detached HEAD, or "" outside a
// repository. It reads HEAD instead of running git.
func gitBranch(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		git := filepath.Join(d, ".git")
		if fi, err := os.Stat(git); err == nil {
			if !fi.IsDir() { // a worktree or submodule: "gitdir: <path>"
				data, _ := os.ReadFile(git)
				path, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
				if !ok {
					return ""
				}
				if !filepath.IsAbs(path) {
					path = filepath.Join(d, path)
				}
				git = path
			}
			data, err := os.ReadFile(filepath.Join(git, "HEAD"))
			if err != nil {
				return ""
			}
			head := strings.TrimSpace(string(data))
			if ref, ok := strings.CutPrefix(head, "ref: "); ok {
				return strings.TrimPrefix(ref, "refs/heads/")
			}
			return head[:min(7, len(head))]
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// UserMessage prints what the user sent as a block on a subtle background
// (a "❯" prefix without colors), followed by a blank line.
func UserMessage(text string) {
	width := termWidth()
	text = strings.ReplaceAll(strings.TrimRight(StripControls(text), "\n"), "\t", "    ")
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
