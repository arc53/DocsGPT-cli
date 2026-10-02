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

// PrintSources lists the sources an answer drew on, on a terminal only: a
// dim "Sources" block of numbered titles, linked (OSC 8) when the source
// has a URL, without duplicates.
func PrintSources(sources []docsgpt.Source) {
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return
	}
	type entry struct{ title, url string }
	var list []entry
	seen := map[string]bool{}
	for _, s := range sources {
		e := entry{title: s.Title}
		if strings.HasPrefix(s.Source, "http://") || strings.HasPrefix(s.Source, "https://") {
			e.url = s.Source
		}
		if e.title == "" {
			e.title = s.Filename
		}
		if e.title == "" && s.Source != "" {
			e.title = path.Base(strings.TrimRight(s.Source, "/"))
		}
		e.title, _, _ = strings.Cut(strings.TrimSpace(e.title), "\n")
		key := e.url + "\x00" + e.title
		if e.title == "" || seen[key] {
			continue
		}
		seen[key] = true
		list = append(list, e)
	}
	if len(list) == 0 {
		return
	}
	links := os.Getenv("TERM") != "dumb"
	var b strings.Builder
	b.WriteString("\n" + T.Dim.Render("Sources") + "\n")
	for i, e := range list[:min(len(list), maxSources)] {
		title := T.Muted.Render(ansi.Truncate(e.title, termWidth()-6, "…"))
		if e.url != "" && links {
			title = ansi.SetHyperlink(e.url) + title + ansi.ResetHyperlink()
		}
		fmt.Fprintf(&b, "  %s %s\n", T.Dim.Render(fmt.Sprintf("%d.", i+1)), title)
	}
	if more := len(list) - maxSources; more > 0 {
		b.WriteString("  " + T.Dim.Render(fmt.Sprintf("+%d more", more)) + "\n")
	}
	fmt.Print(b.String())
}
