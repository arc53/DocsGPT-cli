package display

import (
	"fmt"
	"os"
	"path"
	"strings"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"
)

// maxSources is how many sources are listed before "+N more".
const maxSources = 5

// PrintSources lists the sources an answer drew on, on a terminal only,
// after a blank line (see sourceLines).
func PrintSources(sources []docsgpt.Source) {
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return
	}
	if lines := sourceLines(sources, termWidth()); len(lines) > 0 {
		fmt.Print("\n" + strings.Join(lines, "\n") + "\n")
	}
}

// sourceLines renders the sources in width columns: a dim "Sources" line
// and the numbered titles, linked (OSC 8) when the source has a URL,
// without duplicates; nothing without sources.
func sourceLines(sources []docsgpt.Source, width int) []string {
	type entry struct{ title, url string }
	var list []entry
	seen := map[string]bool{}
	for _, s := range sources {
		e := entry{title: s.Title}
		if u, ok := linkURL(s.Source); ok && !strings.HasPrefix(u, "mailto:") {
			e.url = u
		}
		if e.title == "" {
			e.title = s.Filename
		}
		if e.title == "" && s.Source != "" {
			e.title = path.Base(strings.TrimRight(s.Source, "/"))
		}
		e.title, _, _ = strings.Cut(strings.TrimSpace(e.title), "\n")
		e.title = Safe(e.title)
		key := e.url + "\x00" + e.title
		if e.title == "" || seen[key] {
			continue
		}
		seen[key] = true
		list = append(list, e)
	}
	if len(list) == 0 {
		return nil
	}
	// The title shows either way: linked unless hyperlinks are off.
	links := os.Getenv("TERM") != "dumb" && hyperlinkMode() != "off"
	lines := []string{T.Dim.Render("Sources")}
	for i, e := range list[:min(len(list), maxSources)] {
		title := T.Muted.Render(ansi.Truncate(e.title, width-6, "…"))
		if e.url != "" && links {
			title = openLink(e.url) + title + closeLink
		}
		lines = append(lines, fmt.Sprintf("  %s %s", T.Dim.Render(fmt.Sprintf("%d.", i+1)), title))
	}
	if more := len(list) - maxSources; more > 0 {
		lines = append(lines, "  "+T.Dim.Render(fmt.Sprintf("+%d more", more)))
	}
	return lines
}
