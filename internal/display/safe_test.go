package display

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestSafe(t *testing.T) {
	for in, want := range map[string]string{
		"git status":                        "git status",
		"touch /tmp/x; \x1b[2K\recho hello": "touch /tmp/x; ␛[2K␍echo hello",
		"a\nb":                              "a␊b",
		"bell\a del\x7f":                    "bell␇ del␡",
		"osc \x1b]8;;http://e\x07x":         "osc ␛]8;;http://e␇x",
		"c1 \u009b31m":                      `c1 \u009b31m`,
		"rlo \u202eexe.txt":                 `rlo \u202eexe.txt`,
		"zw\u200bsp":                        `zw\u200bsp`,
		"bad \x9b utf8":                     "bad \ufffd utf8",
		"tab\there":                         "tab    here",
		"ünïcödé ✓":                         "ünïcödé ✓",
	} {
		if got := Safe(in); got != want {
			t.Errorf("Safe(%q) = %q, want %q", in, got, want)
		}
	}
}

// stderrOf captures what f writes to os.Stderr.
func stderrOf(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// TestToolBlockSpoofing: a command cannot hide part of itself from the
// approval prompt with escape sequences.
func TestToolBlockSpoofing(t *testing.T) {
	UsePlainTheme()
	defer InitTheme("dark")
	out := stderrOf(t, func() {
		ToolTitle("$ touch /tmp/x; \x1b[2K\recho hello\nsecond\rline", "in \x1b]0;t\x07dir")
		ToolStatus(false, "open \x1b[8mhidden: no such file")
	})
	if strings.ContainsAny(out, "\x1b\r\a") {
		t.Errorf("control characters reached the terminal: %q", out)
	}
	for _, want := range []string{"$ touch /tmp/x; ␛[2K␍echo hello", "  second␍line in ␛]0;t␇dir", "open ␛[8mhidden"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q lacks %q", out, want)
		}
	}

	preview, _, _ := DiffPreview("", "ok\n\x1b[1A\x1b[2Kfake\n", 10)
	if joined := strings.Join(preview, "\n"); strings.Contains(joined, "\x1b") || !strings.Contains(joined, "␛[1A␛[2Kfake") {
		t.Errorf("diff preview: %q", joined)
	}
}

func TestLinkable(t *testing.T) {
	for u, want := range map[string]bool{
		"https://docs.example.com/a?b=c#d":      true,
		"http://x":                              true,
		"ftp://x":                               false,
		"https://x\x07\x1b]8;;https://evil\x07": false,
		"https://x\x1b\\injected":               false,
		"https://x/\u009c":                      false,
		"https://x/ space":                      false,
		"javascript:alert(1)":                   false,
	} {
		if got := linkable(u); got != want {
			t.Errorf("linkable(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestStripControls(t *testing.T) {
	for in, want := range map[string]string{
		"plain\ttext\nline two ✓":                "plain\ttext\nline two ✓",
		"red \x1b[31mtext\x1b[0m":                "red text",
		"conceal \x1b[8msecret\x1b[28m":          "conceal secret",
		"title \x1b]0;pwned\x07after":            "title after",
		"clip \x1b]52;c;cm0gLXJmIH4=\x1b\\after": "clip after",
		"palette \x1b]4;1;rgb:ff/00/00\x1b\\x":   "palette x",
		"cursor \x1b[2A\x1b[2Kup":                "cursor up",
		"dcs \x1bP1$r0m\x1b\\x":                  "dcs x",
		"charset \x1b(Bx \x1bcreset":             "charset x reset",
		"c1 \u009b31mred \u009d0;t\u009cx":       "c1 red x",
		"crlf\r\nover\rwrite\b":                  "crlf\noverwrite",
		"abort \x1b]0;t\x1b[1mbold":              "abort bold",
		"bell\a del\x7f nul\x00":                 "bell del nul",
		"csi \x1b[日本":                            "csi 日本",
		"unterminated \x1b]8;;http://e":          "unterminated ",
	} {
		if got := StripControls(in); got != want {
			t.Errorf("StripControls(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestControlFilterSplit feeds sequences one byte at a time: a sequence
// split across stream chunks is removed whole.
func TestControlFilterSplit(t *testing.T) {
	in := "a\x1b]52;c;eA==\x1b\\b\x1b[1;31mc\u009b2Jd"
	var f controlFilter
	var got strings.Builder
	for _, r := range in {
		got.WriteString(f.clean(string(r)))
	}
	if got.String() != "abcd" {
		t.Errorf("split: %q", got.String())
	}
}
