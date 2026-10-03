package display

import (
	"os"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

const copyMD = "## Setting things up quickly\n\n" +
	"The installer puts the binary on your PATH and checks a well-known-hyphenated-compound. It takes a minute.\n\n" +
	"- first item that is long enough to wrap around the narrow terminal\n- second item\n  - nested item that wraps around the narrow width too\n\n" +
	"```go\nfunc main() {\n    fmt.Println(\"a long line of code that does not fit in the width\")\n}\n```\n\n" +
	"> A quote long enough to wrap around the narrow terminal width\n\n" +
	"中文字符测试中文字符测试中文字符测试中文字符测试 ok\n\n" +
	"1. **Install** it,\n   then check.\n\n   Run `docsgpt-cli --version`, then `docsgpt-cli`.\n\n2. Second step\n\n" +
	"See https://github.com/arc53/DocsGPT-cli/releases/download/v1.2.3/docsgpt-cli_darwin_arm64.tar.gz or id 6f0e1c2a-1111-2222-3333-444455556666.\n\n" +
	"> A quote with `Bash`, `Zsh`, and a [link](https://example.com), long enough to wrap.\n"

// withColors renders with true colours, or none.
func withColors(t *testing.T, on bool) {
	t.Setenv("COLORTERM", "truecolor")
	p := termenv.TrueColor
	if !on {
		t.Setenv("NO_COLOR", "1")
		p = termenv.Ascii
	}
	termenv.SetDefaultOutput(termenv.NewOutput(os.Stdout, termenv.WithTTY(on)))
	lipgloss.SetColorProfile(p)
	InitTheme("dark")
	mdRenderer = nil
}

// restoreColors puts the colours back after the test.
func restoreColors(t *testing.T) {
	out := termenv.DefaultOutput()
	t.Cleanup(func() { termenv.SetDefaultOutput(out); InitTheme("dark"); mdRenderer = nil })
}

// copied is the whole of a block as it copies.
func copied(b ui.Block, width int) string {
	return ui.Unwrap(b.Lines(width), b.(ui.Plainer).Plain(width), 0, 1<<30)
}

// TestCopyAnswer: a copied answer is plain text, its paragraphs, list items
// and quotes one line each again, its code lines whole and unindented.
func TestCopyAnswer(t *testing.T) {
	restoreColors(t)
	for _, colors := range []bool{true, false} {
		withColors(t, colors)
		a := NewAnswer(false)
		a.Delta(docsgpt.Delta{Content: copyMD})
		a.Finish()
		if rows := strings.Join(a.Lines(30), "\n"); colors != strings.Contains(rows, "│ ") || colors != strings.Contains(rows, "\x1b[") {
			t.Fatalf("colors %v not in effect:\n%s", colors, rows)
		}
		h2, nest, strong := "", "  ", "" // glamour's plain style differs
		if !colors {
			h2, nest, strong = "## ", "    ", "**"
		}
		want := h2 + "Setting things up quickly\n\n" +
			"The installer puts the binary on your PATH and checks a well-known-hyphenated-compound. It takes a minute.\n\n" +
			"• first item that is long enough to wrap around the narrow terminal\n• second item\n" + nest + "• nested item that wraps around the narrow width too\n\n" +
			"```go\nfunc main() {\n    fmt.Println(\"a long line of code that does not fit in the width\")\n}\n```\n\n" +
			"A quote long enough to wrap around the narrow terminal width\n\n" +
			"中文字符测试中文字符测试中文字符测试中文字符测试 ok\n\n" +
			"1. " + strong + "Install" + strong + " it, then check.\n   Run docsgpt-cli --version, then docsgpt-cli.\n\n2. Second step\n\n" +
			"See https://github.com/arc53/DocsGPT-cli/releases/download/v1.2.3/docsgpt-cli_darwin_arm64.tar.gz or id 6f0e1c2a-1111-2222-3333-444455556666.\n\n" +
			"A quote with Bash, Zsh, and a link https://example.com, long enough to wrap."
		for _, w := range []int{30, 41, 120} {
			if got := copied(a, w); got != want {
				t.Errorf("colors %v, width %d:\n%s\n--- rows:\n%s", colors, w, got, strings.Join(a.Lines(w), "\n"))
			}
		}
		if got := copied(Markdown(copyMD), 30); got != want {
			t.Errorf("colors %v, resumed:\n%s", colors, got)
		}
	}
}

// TestCopyBlocks: a message, a tool call and a notice copy as their text,
// without the background's padding or indents, wrapped lines joined.
func TestCopyBlocks(t *testing.T) {
	restoreColors(t)
	msg := "a message long enough to wrap around the narrow width\n    indented second line"
	for _, colors := range []bool{true, false} {
		withColors(t, colors)
		if got := copied(User(msg), 24); got != msg {
			t.Errorf("colors %v, message:\n%s\n--- rows:\n%s", colors, got, strings.Join(User(msg).Lines(24), "\n"))
		}
		b := NewToolBlock("$ make build-everything-with-a-long-target-name now", "in /src")
		b.Write([]byte("ok\n    indented output\n"))
		b.Close(true, "exit 0 · 1.2s")
		want := "$ make build-everything-with-a-long-target-name now in /src\nok\n    indented output\n✓ exit 0 · 1.2s"
		if got := copied(b, 30); got != want {
			t.Errorf("colors %v, tool:\n%s\n--- rows:\n%s", colors, got, strings.Join(b.Lines(30), "\n"))
		}
		note := "Reasoning will be shown from now on, in every answer."
		if got := copied(Note(note), 20); got != note {
			t.Errorf("colors %v, note: %q", colors, got)
		}
	}
}
