package display

import (
	"bytes"
	"strings"
	"testing"
	"unicode"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	xansi "github.com/charmbracelet/x/ansi"
)

// rendered is md as the chat and ask show it at width, without styles.
func rendered(md string, width int) string {
	return xansi.Strip(renderMarkdown(newMarkdown(width), width, md, nil))
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

func TestMarkdownLooseList(t *testing.T) {
	eachStyle(t, func(colors bool) {
		for _, w := range []int{40, 60, 100} {
			got := rendered("1. **First** step\n\n   More about it.\n\n2. Second step\n\n3. Third\n", w)
			want := "1. First step\n   More about it.\n\n2. Second step\n\n3. Third"
			if !colors {
				want = "1. **First** step\n   More about it.\n\n2. Second step\n\n3. Third"
			}
			if got != want {
				t.Errorf("colors %v, width %d:\n%s", colors, w, got)
			}
		}
	})
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

func TestMarkdownLists(t *testing.T) {
	tests := []struct{ md, want string }{
		{"- outer\n  - nested\n    - deeper\n- back\n", "• outer\n  • nested\n    • deeper\n• back"},
		{"5. five\n6. six\n", "5. five\n6. six"},
		{"- [x] done task\n- [ ] open task\n", "[✓] done task\n[ ] open task"},
		{"## Heading\n- list right after heading\n- second\n", "Heading\n\n• list right after heading\n• second"},
		{"1. step with code:\n\n   ```bash\n   echo hi\n   ```\n\n2. next step\n", "1. step with code:\n  echo hi\n\n2. next step"},
		{"- first line  \n  second line after a hard break\n- b\n", "• first line\n  second line after a hard break\n• b"},
		{"1. a\n\n   - x\n   - y\n\n2. b\n", "1. a\n  • x\n  • y\n\n2. b"},
		{"> - a\n>\n> - b\n", "│ • a\n│\n│ • b"},
	}
	eachStyle(t, func(colors bool) {
		if !colors {
			return // glamour's plain style indents and marks differently
		}
		for _, tt := range tests {
			if got := rendered(tt.md, 60); got != tt.want {
				t.Errorf("%q:\n%s\nwant\n%s", tt.md, got, tt.want)
			}
		}
	})
	// A long item hangs under its text, the marker's width whatever it is.
	md := "10. an item long enough to wrap around the narrow terminal width\n- [ ] a task long enough to wrap around the narrow terminal width"
	for _, w := range []int{20, 30} {
		out := rendered(md, w)
		checkRows(t, out, w)
		for _, row := range strings.Split(out, "\n")[1:] {
			if row != "" && !strings.HasPrefix(row, "    ") && !strings.HasPrefix(row, "[ ] ") {
				t.Errorf("width %d: not hung:\n%s", w, out)
			}
		}
	}
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

// quirksMD holds the cases glamour got wrong, and the copy test's.
var quirksMD = copyMD + "\n" + strings.Join([]string{
	"1. first\n\n2. second\n\n3. third",
	"- outer\n  - nested\n    - deeper\n- back",
	"5. five\n6. six",
	"## Heading\n- [x] done\n- [ ] open",
	"1. step with code:\n\n   ```bash\n   echo hi\n   ```\n\n2. next step",
	"> - a\n>\n> - b",
	"1. a\n\n   - x\n   - y\n\n2. b",
	"| a | b |\n|---|---|\n| 1 | 2 |",
}, "\n\n") + "\n"

// listsMD holds loose ordered lists, numbered lazily, explicitly, wrongly
// and from N, with the blocks that end a list between them: a paragraph, a
// fence, the other delimiter.
var listsMD = strings.Join([]string{
	"1. lazy a\n\n1. lazy b\n\n1. lazy c",
	"1. explicit a\n\n2. explicit b\n\n3. explicit c",
	"5. from five\n\n5. then six\n\n   with a second paragraph\n\n9. then seven",
	"1. odd a\n\n3. odd b",
	"1) paren a\n\n1) paren b",
	"1. dot\n\n1) paren after a dot",
	"1. before a fence\n\n```\ncode\n```\n\n1. after a fence",
	"8. a long run\n\n1. b\n\n1. c\n\n1. ten, a wider marker\n   that wraps around the narrow terminal width\n\n1. eleven",
}, "\n\nThen a paragraph, which ends the list.\n\n") + "\n"

// TestMarkdownStreamsAsWhole: an answer streamed in chunks of any size,
// rendered block by block, ends the same as rendered whole (a resumed chat),
// in the chat and in ask.
func TestMarkdownStreamsAsWhole(t *testing.T) {
	for _, md := range []string{quirksMD, listsMD} {
		streamsAsWhole(t, md)
	}
}

func streamsAsWhole(t *testing.T, md string) {
	eachStyle(t, func(colors bool) {
		for _, w := range []int{40, 60, 100} {
			want := xansi.Strip(strings.Join(Markdown(md).Lines(w), "\n"))
			// ask leaves a line too long for the screen to the terminal
			wantAsk := emulate(t, renderMarkdown(newMarkdown(w), w, md, nil), w, 1000)
			runes := []rune(md) // deltas are whole characters
			for _, size := range []int{1, 3, 7, 64} {
				a := NewAnswer(false)
				for i := 0; i < len(runes); i += size {
					a.Delta(docsgpt.Delta{Content: string(runes[i:min(i+size, len(runes))])})
					a.Lines(w)
				}
				a.Finish()
				if got := xansi.Strip(strings.Join(a.Lines(w), "\n")); got != want {
					t.Errorf("colors %v, width %d, chunks of %d:\n%s\n--- want\n%s", colors, w, size, got, want)
				}
				if size < 7 {
					continue // ask's emulated screen is slow to replay
				}
				var out bytes.Buffer
				r := &StreamRenderer{out: &out, tty: true, width: w, height: 1000, md: newMarkdown(w)}
				for i := 0; i < len(runes); i += size {
					r.Delta(docsgpt.Delta{Content: string(runes[i:min(i+size, len(runes))])})
					r.lastDraw = r.lastDraw.Add(-frameInterval)
				}
				r.Flush()
				if got := emulate(t, out.String(), w, 1000); got != wantAsk {
					t.Errorf("ask: colors %v, width %d, chunks of %d:\n%s\n--- want\n%s", colors, w, size, got, wantAsk)
				}
			}
		}
	})
}
