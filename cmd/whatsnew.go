package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/update"
)

// whatsNewLines is how much of the release notes the chat shows after an
// update; /changelog shows them all.
const whatsNewLines = 10

// whatsNew shows, once, what is new in the release an update installed
// (unless the whats_new setting is off).
func (s *chatSession) whatsNew() {
	if s.cfg.Settings.WhatsNew == "off" {
		return
	}
	notes, ok := update.PendingNotes(Version)
	if !ok {
		return
	}
	update.ShownNotes()
	s.scr.Add(display.Note(whatsNewText(notes, whatsNewLines)))
}

// whatsNewText is the start of notes: a title and up to max lines.
func whatsNewText(notes update.Notes, max int) string {
	lines := strings.Split(display.StripControls(update.TidyNotes(notes.Body)), "\n")
	var b strings.Builder
	b.WriteString("What's new in " + notes.Version)
	for i, l := range lines {
		if i == max {
			n := len(lines) - max
			fmt.Fprintf(&b, "\n… %d more %s · /changelog", n, plural(n, "line", "lines"))
			break
		}
		if rest, ok := strings.CutPrefix(l, "* "); ok {
			l = "• " + rest // as /changelog's markdown has it
		} else if rest, ok := strings.CutPrefix(l, "- "); ok {
			l = "• " + rest
		}
		b.WriteString("\n" + l)
	}
	return b.String()
}

// changelog shows the latest release notes, fetched when the last check is
// old (Esc stops that); offline it says where they are.
func (s *chatSession) changelog(string) {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	s.scr.Busy("Fetching the release notes…", cancel)
	notes, err := update.LatestNotes(ctx)
	s.scr.Busy("", nil)
	if err != nil {
		s.fail("Could not fetch the release notes; they are at " + update.ReleasesPage)
		return
	}
	title := "What's new in " + notes.Version
	if update.IsNewer(notes.Version, Version) {
		title += display.Dim(" · you have " + Version + "; docsgpt-cli update installs it")
	}
	s.scr.Add(display.Text(display.Accent(title)))
	body := update.TidyNotes(notes.Body)
	if body == "" {
		body = "No notes for this release."
	}
	s.scr.Add(display.Markdown(body))
	if notes.URL != "" {
		s.scr.Add(display.Note(notes.URL))
	}
}
