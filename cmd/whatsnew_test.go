package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/ui"
	"github.com/arc53/DocsGPT-cli/internal/update"
)

func TestWhatsNew(t *testing.T) {
	isolateConfig(t)
	old := Version
	Version = "1.3.0"
	t.Cleanup(func() { Version = old })

	var body strings.Builder
	body.WriteString("## Changelog\n")
	for i := 1; i <= 14; i++ {
		fmt.Fprintf(&body, "* %07x feat: change %d\n", 0xabc0000+i, i)
	}
	update.RecordCheck(&update.Release{TagName: "v1.3.0", Body: body.String()})
	update.MarkUpdated("v1.3.0")

	shown := func(s *chatSession) string { return s.scr.Transcript(80) }
	newSession := func() *chatSession {
		return &chatSession{scr: ui.NewScreen(ui.ScreenOptions{Headless: true})}
	}

	s := newSession()
	s.cfg.Settings.WhatsNew = "off"
	if s.whatsNew(); strings.Contains(shown(s), "What's new") {
		t.Fatal("shown with whats_new off")
	}

	s = newSession()
	s.whatsNew()
	got := shown(s)
	for _, want := range []string{"What's new in v1.3.0", "• feat: change 1", "• feat: change 10", "… 4 more lines · /changelog"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "change 11") || strings.Contains(got, "abc") {
		t.Errorf("too much, or hashes: %q", got)
	}

	s = newSession()
	if s.whatsNew(); strings.Contains(shown(s), "What's new") {
		t.Fatal("shown twice")
	}
}
