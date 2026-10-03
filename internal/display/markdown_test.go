package display

import (
	"strings"
	"testing"
	"unicode"

	xansi "github.com/charmbracelet/x/ansi"
)

// rendered is md as the chat and ask show it at width, without styles.
func rendered(md string, width int) string {
	return xansi.Strip(renderMarkdown(newMarkdown(width), width, md))
}

// eachStyle runs f with colours on and off.
func eachStyle(t *testing.T, f func(colors bool)) {
	restoreColors(t)
	for _, colors := range []bool{true, false} {
		withColors(t, colors)
		f(colors)
	}
}

// checkRows fails on a row wider than width, ending in a space, or holding
// nothing but punctuation.
func checkRows(t *testing.T, out string, width int) {
	t.Helper()
	for _, row := range strings.Split(out, "\n") {
		if w := xansi.StringWidth(row); w > width {
			t.Errorf("width %d: a row of %d columns: %q", width, w, row)
		}
		if strings.HasSuffix(row, " ") {
			t.Errorf("width %d: trailing space: %q", width, row)
		}
		if s := strings.Trim(row, " │|"); s != "" && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPunct(r) }) < 0 {
			t.Errorf("width %d: a row of punctuation only: %q", width, row)
		}
	}
}

// checkGreedy fails when a row breaks before a word that would fit on it;
// out is one paragraph.
func checkGreedy(t *testing.T, out string, width int) {
	t.Helper()
	rows := strings.Split(out, "\n")
	for i := 0; i+1 < len(rows); i++ {
		next := strings.Fields(rows[i+1])
		if len(next) > 0 && xansi.StringWidth(rows[i])+1+xansi.StringWidth(next[0]) <= width {
			t.Errorf("width %d: %q breaks before %q", width, rows[i], next[0])
		}
	}
}

func TestMarkdownPunctuation(t *testing.T) {
	md := "Shells: `Bash`, `Zsh`, and `Fish` are supported; see [the docs](https://example.com), **bold**, *em*, and `x`."
	eachStyle(t, func(colors bool) {
		for w := 20; w <= 100; w++ {
			out := rendered(md, w)
			checkRows(t, out, w)
			for _, row := range strings.Split(out, "\n") {
				if strings.IndexAny(row, ",.;") == 0 {
					t.Errorf("colors %v, width %d: a row starts with punctuation:\n%s", colors, w, out)
				}
			}
		}
	})
}

// TestMarkdownWrapsGreedily: hyphens, inline code and soft breaks do not
// make a paragraph break early or overflow (and orphan a word on a row).
func TestMarkdownWrapsGreedily(t *testing.T) {
	for _, md := range []string{
		"Inline code at the end of line `docsgpt-cli --version`\nand next line, then a well-known-hyphenated-compound and more-hyphen-words to-wrap here.",
		"Some *emphasis* and **strong** text with `code` and a [link](https://example.com) in between, repeated: *emphasis*, **strong**, `code`.",
	} {
		eachStyle(t, func(colors bool) {
			for w := 30; w <= 100; w++ {
				out := rendered(md, w)
				checkRows(t, out, w)
				checkGreedy(t, out, w)
			}
		})
	}
}

func TestMarkdownQuote(t *testing.T) {
	md := "> A quote long enough to wrap around the narrow terminal width, and then some more words to wrap again.\n>\n> A second paragraph."
	eachStyle(t, func(colors bool) {
		bar := "│"
		if !colors {
			bar = "|"
		}
		for w := 20; w <= 100; w++ {
			out := rendered(md, w)
			checkRows(t, out, w)
			for _, row := range strings.Split(out, "\n") {
				if !strings.HasPrefix(row, bar) {
					t.Errorf("colors %v, width %d: a row without its bar:\n%s", colors, w, out)
				}
			}
		}
	})
}

func TestMarkdownLongWords(t *testing.T) {
	url := "https://github.com/arc53/DocsGPT-cli/releases/download/v1.2.3/docsgpt-cli_darwin_arm64.tar.gz"
	uuid := "6f0e1c2a-1111-2222-3333-444455556666"
	md := "See " + url + " for the archive. The id is " + uuid + " and the path /usr/local/lib/some-long-hyphenated-directory-name/file.txt here."
	eachStyle(t, func(colors bool) {
		for w := 40; w <= 100; w++ {
			out := rendered(md, w)
			checkRows(t, out, w)
			if !strings.Contains(out, uuid) {
				t.Errorf("width %d: the id is broken:\n%s", w, out)
			}
			if !strings.Contains(strings.ReplaceAll(out, "\n", ""), url) {
				t.Errorf("width %d: the URL is broken up:\n%s", w, out)
			}
			checkGreedy(t, out, w)
		}
	})
}

func TestMarkdownWide(t *testing.T) {
	md := "Emoji 🎉 and wide 🚀 chars, CJK 中文字符测试中文字符测试中文字符测试中文字符测试 ok.\n\n" +
		"| Column one | Column two that is long | Column three is even longer than the others | Four |\n|---|---|---|---|\n| a | b | c | d |\n| some long cell text here | more text | still more long text in this cell | x |\n"
	eachStyle(t, func(colors bool) {
		for _, w := range []int{40, 60, 100} {
			out := rendered(md, w)
			checkRows(t, out, w)
			if !strings.Contains(strings.ReplaceAll(out, "\n", ""), "中文字符测试中文字符测试中文字符测试中文字符测试") {
				t.Errorf("width %d: CJK lost:\n%s", w, out)
			}
		}
		if out := rendered(md, 100); !strings.Contains(out, "Column one") || !strings.Contains(out, "still more long text in this cell") {
			t.Errorf("table at 100:\n%s", out)
		}
	})
}
