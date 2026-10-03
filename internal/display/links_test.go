package display

import (
	"os"
	"regexp"
	"strings"
	"testing"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	xansi "github.com/charmbracelet/x/ansi"
)

// The tests render without hyperlinks unless they turn them on: the
// terminal they run in must not decide.
func TestMain(m *testing.M) {
	os.Setenv("DOCSGPT_HYPERLINKS", "0")
	os.Exit(m.Run())
}

func withHyperlinks(t *testing.T, on bool) {
	t.Helper()
	v := "0"
	if on {
		v = "1"
	}
	t.Setenv("DOCSGPT_HYPERLINKS", v)
}

var osc8 = regexp.MustCompile("\x1b\\]8;[^\x07\x1b]*(?:\x07|\x1b\\\\)")

// linkRows checks that every row opens and closes its own hyperlinks, and
// returns the text each URL covers, rows joined by a space.
func linkRows(t *testing.T, rows []string) map[string]string {
	t.Helper()
	covered := map[string]string{}
	for _, row := range rows {
		open, prev := "", ""
		rest := row
		for rest != "" {
			loc := osc8.FindStringIndex(rest)
			text := rest
			if loc != nil {
				text = rest[:loc[0]]
			}
			if plain := xansi.Strip(text); open != "" && plain != "" {
				if prev == open && covered[open] != "" {
					covered[open] += plain
				} else if covered[open] != "" {
					covered[open] += " " + plain
				} else {
					covered[open] = plain
				}
				prev = open
			}
			if loc == nil {
				break
			}
			u, _ := linkSeq(rest[loc[0]:loc[1]])
			if u != "" && open != "" {
				t.Errorf("a link opened inside another: %q", row)
			}
			open = u
			rest = rest[loc[1]:]
		}
		if open != "" {
			t.Errorf("row ends inside a link: %q", row)
		}
	}
	return covered
}

func TestLinksWrap(t *testing.T) {
	withHyperlinks(t, true)
	md := "Read [the installation guide for every platform](https://docs.example.com/install?os=all) first, or see https://example.com/a/very/long/path/to/a/page.html and <mailto:help@example.com> or help@example.com."
	eachStyle(t, func(colors bool) {
		for w := 20; w <= 120; w += 7 {
			out := renderMarkdown(newMarkdown(w), w, md, nil)
			rows := strings.Split(out, "\n")
			checkRows(t, xansi.Strip(out), w)
			for _, r := range rows {
				if xansi.StringWidth(r) > w {
					t.Errorf("width %d: row too wide: %q", w, r)
				}
			}
			got := linkRows(t, rows)
			want := map[string]string{
				"https://docs.example.com/install?os=all":             "the installation guide for every platform",
				"https://example.com/a/very/long/path/to/a/page.html": "https://example.com/a/very/long/path/to/a/page.html",
				"mailto:help@example.com":                             "mailto:help@example.com help@example.com",
			}
			for u, text := range want {
				g := got[u]
				// a URL longer than the row is cut, not broken at a space
				if strings.ReplaceAll(g, " ", "") != strings.ReplaceAll(text, " ", "") {
					t.Errorf("colors %v, width %d: %s covers %q, want %q\n%s", colors, w, u, g, text, out)
				}
			}
			if len(got) != len(want) {
				t.Errorf("colors %v, width %d: links %v", colors, w, got)
			}
			if plain := xansi.Strip(out); strings.Contains(plain, "docs.example.com") {
				t.Errorf("width %d: the URL of a titled link shows:\n%s", w, plain)
			}
		}
	})
}

func TestLinksOff(t *testing.T) {
	withHyperlinks(t, false)
	md := "Read [the guide](https://docs.example.com/install), see https://example.com/x and <mailto:help@example.com>, [top](#top), [same](https://example.com/same)."
	eachStyle(t, func(colors bool) {
		out := renderMarkdown(newMarkdown(200), 200, md, nil)
		if strings.Contains(out, "\x1b]8") {
			t.Errorf("hyperlinks while off: %q", out)
		}
		want := "Read the guide (https://docs.example.com/install), see https://example.com/x and mailto:help@example.com, top, same (https://example.com/same)."
		if got := xansi.Strip(out); got != want {
			t.Errorf("colors %v:\n%s\nwant\n%s", colors, got, want)
		}
	})
}

// TestLinksSafe: the model cannot make a hyperlink of its own, smuggle a
// marker in, or have a javascript: or file: link made clickable.
func TestLinksSafe(t *testing.T) {
	withHyperlinks(t, true)
	evil := "click \x1b]8;;https://evil.example\x1b\\here\x1b]8;;\x1b\\ and \x1b]8;;https://evil.example\a there\x1b]8;;\a, " +
		"\uFDD0marker\uFDD1, [js](javascript:alert(1)), [file](file:///etc/passwd), [rel](/docs/x), " +
		"[ctl](<https://ok.example/aüb>) and [fine](https://ok.example)."
	for name, rows := range map[string][]string{
		"markdown": Markdown(evil).Lines(60),
		"answer": func() []string {
			a := NewAnswer(false)
			for _, r := range evil {
				a.Delta(docsgpt.Delta{Content: string(r)})
			}
			a.Finish()
			return a.Lines(60)
		}(),
	} {
		out := strings.Join(rows, "\n")
		if strings.Contains(out, "evil") {
			t.Errorf("%s: the model's link survived: %q", name, out)
		}
		got := linkRows(t, rows)
		want := map[string]string{"https://ok.example/a%C3%BCb": "ctl", "https://ok.example": "fine"}
		if len(got) != len(want) || got["https://ok.example"] != "fine" || got["https://ok.example/a%C3%BCb"] != "ctl" {
			t.Errorf("%s: links %q, want %q", name, got, want)
		}
		plain := xansi.Strip(out)
		for _, s := range []string{"js (javascript:alert(1))", "file (file:///etc/passwd)", "rel (/docs/x)", "marker,"} {
			if !strings.Contains(strings.ReplaceAll(plain, "\n", " "), s) {
				t.Errorf("%s: %q missing in\n%s", name, s, plain)
			}
		}
		if strings.ContainsAny(out, "\uFDD0\uFDD1") {
			t.Errorf("%s: a marker shows: %q", name, out)
		}
	}
}

// TestLinksCopy: a link copies as its text where it is a hyperlink, as
// shown ("text (url)") where it is not.
func TestLinksCopy(t *testing.T) {
	md := "See [the guide to installing it everywhere](https://docs.example.com/install) for more, or https://example.com/x."
	for _, on := range []bool{true, false} {
		withHyperlinks(t, on)
		want := "See the guide to installing it everywhere for more, or https://example.com/x."
		if !on {
			want = "See the guide to installing it everywhere (https://docs.example.com/install) for more, or https://example.com/x."
		}
		for _, w := range []int{24, 40, 200} {
			if got := copied(Markdown(md), w); got != want {
				t.Errorf("hyperlinks %v, width %d: %q", on, w, got)
			}
		}
	}
}

func TestRelink(t *testing.T) {
	a, b := openLink("https://a"), openLink("https://b")
	for in, want := range map[string]string{
		"plain":                       "plain",
		a + "x" + closeLink + " y":    a + "x" + closeLink + " y",
		a + closeLink + b + "x":       b + "x" + closeLink,                    // a cut row: what it shows decides
		"\x1b[1m" + a + "x\x1b[0m":    "\x1b[1m" + a + "x\x1b[0m" + closeLink, // closed at the row's end
		a + "x" + b + "y" + closeLink: a + "x" + closeLink + b + "y" + closeLink,
		"x" + a + closeLink:           "x",
		"\x1b]8;;https://a\ax":        a + "x" + closeLink,
	} {
		if got := relink(in); got != want {
			t.Errorf("relink(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectHyperlinks(t *testing.T) {
	for _, k := range []string{"TMUX", "TERM", "TERM_PROGRAM", "KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE", "WARP_SESSION_ID", "ITERM_SESSION_ID", "LC_TERMINAL", "WT_SESSION"} {
		t.Setenv(k, "")
	}
	for _, c := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, false},
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, true},
		{map[string]string{"LC_TERMINAL": "iTerm2"}, true},
		{map[string]string{"TERM_PROGRAM": "ghostty"}, true},
		{map[string]string{"TERM": "xterm-kitty"}, true},
		{map[string]string{"TERM_PROGRAM": "WezTerm"}, true},
		{map[string]string{"TERM_PROGRAM": "vscode"}, true},
		{map[string]string{"WT_SESSION": "x"}, true},
		{map[string]string{"TERM": "screen-256color", "TERM_PROGRAM": "iTerm.app"}, false},
		{map[string]string{"TERM": "dumb", "TERM_PROGRAM": "iTerm.app"}, false},
		{map[string]string{"TERM": "xterm-256color"}, false},
	} {
		for k, v := range c.env {
			t.Setenv(k, v)
		}
		if got := detectHyperlinks(); got != c.want {
			t.Errorf("%v: %v", c.env, got)
		}
		for k := range c.env {
			t.Setenv(k, "")
		}
	}

	t.Setenv("DOCSGPT_HYPERLINKS", "")
	SetHyperlinks("on")
	defer SetHyperlinks("")
	if !hyperlinks() {
		t.Error("setting on: off")
	}
	t.Setenv("DOCSGPT_HYPERLINKS", "0")
	if hyperlinks() {
		t.Error("DOCSGPT_HYPERLINKS=0 over the setting: on")
	}
	t.Setenv("DOCSGPT_HYPERLINKS", "auto")
	SetHyperlinks("off")
	if hyperlinks() {
		t.Error("auto from the environment, off in the setting: on")
	}
}

// TestLinksStream: with hyperlinks, an answer streamed in chunks still
// ends as rendered whole, in the chat and in ask.
func TestLinksStream(t *testing.T) {
	withHyperlinks(t, true)
	streamsAsWhole(t, "See [the guide](https://docs.example.com/install) and https://example.com/x.\n\n"+
		"- an item with [a link that wraps around the narrow terminal width](https://example.com/item)\n"+
		"- <https://example.com/auto>\n\n> quoted [link](https://example.com/q) here\n")
}
