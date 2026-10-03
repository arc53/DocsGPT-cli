package display

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	xansi "github.com/charmbracelet/x/ansi"
)

func TestCommitPoint(t *testing.T) {
	tests := []struct {
		name, text string
		want       int
	}{
		{"single paragraph", "Hello world", 0},
		{"blank line, next block not begun", "Para one.\n\n", 0},
		{"next block begun", "Para one.\n\nPara", len("Para one.\n\n")},
		{"last of several", "A\n\nB\n\nC", len("A\n\nB\n\n")},
		{"indented continuation", "- item\n\n  more", 0},
		{"blank line inside a fence", "```\na\n\nb\n", 0},
		{"after a closing fence", "```sh\nls\n```\nText", len("```sh\nls\n```\n")},
		{"indented fence closes no block", "- a\n  ```\n  x\n  ```\nb", 0},
		{"tilde fence", "~~~\n\n~~~\n\nx", len("~~~\n\n~~~\n\n")},
		{"inline code is no fence", "```x``` y\n\nz", len("```x``` y\n\n")},
		{"partial blank line", "A\n\n  ", 0},
	}
	for _, tt := range tests {
		if got := commitPoint(tt.text); got != tt.want {
			t.Errorf("%s: commitPoint(%q) = %d, want %d", tt.name, tt.text, got, tt.want)
		}
	}
}

const sample = "# Install\n\nRun the installer, then check the version:\n\n" +
	"```bash\ncurl -fsSL https://example.com/install.sh | sh\ndocsgpt-cli --version\n```\n\n" +
	"Then:\n\n- first item that is long enough to wrap around the narrow terminal\n- second item\n\n" +
	"| Flag | Meaning |\n|---|---|\n| --url | server |\n\nDone, **enjoy**.\n"

// TestStreamRendererScreen streams sample in chunks of various sizes into a
// small fake terminal and checks the final screen is the same every time:
// no block printed twice, nothing left behind by the in-place redraws.
func TestStreamRendererScreen(t *testing.T) {
	for _, height := range []int{40, 6} { // 6: blocks taller than the screen
		var want string
		for _, size := range []int{len(sample), 1, 3, 7} {
			var out bytes.Buffer
			r := &StreamRenderer{out: &out, tty: true, width: 40, height: height, md: newMarkdown(40)}
			for i := 0; i < len(sample); i += size {
				r.Delta(docsgpt.Delta{Content: sample[i:min(i+size, len(sample))]})
				r.lastDraw = r.lastDraw.Add(-frameInterval) // no throttling in the test
			}
			r.Flush()

			screen := emulate(t, out.String(), 40, height)
			if size == len(sample) {
				want = screen
				for _, s := range []string{"Install", "docsgpt-cli --version", "second item", "Done, "} {
					if strings.Count(screen, s) != 1 {
						t.Fatalf("height %d: %q appears %d times in:\n%s", height, s, strings.Count(screen, s), screen)
					}
				}
				continue
			}
			if screen != want {
				t.Errorf("height %d, chunks of %d: screen differs\n--- got\n%s\n--- want\n%s", height, size, screen, want)
			}
			if r.Content() != sample {
				t.Errorf("Content() lost text")
			}
		}
	}
}

func TestStreamRendererNoTrailingSpaces(t *testing.T) {
	var out bytes.Buffer
	r := &StreamRenderer{out: &out, tty: true, width: 60, height: 40, md: newMarkdown(60)}
	r.Delta(docsgpt.Delta{Content: sample})
	r.Flush()
	for _, line := range strings.Split(escape.ReplaceAllString(out.String(), ""), "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line ends with a space: %q", line)
		}
	}
}

func TestStreamRendererRawWhenNotATerminal(t *testing.T) {
	var out bytes.Buffer
	r := &StreamRenderer{out: &out, ShowReasoning: true}
	r.Delta(docsgpt.Delta{ReasoningContent: "hmm"})
	r.Delta(docsgpt.Delta{Content: "# Title\n\nbody"})
	r.Flush()
	if got := out.String(); got != "# Title\n\nbody\n" {
		t.Errorf("output = %q, want the raw answer plus a final newline", got)
	}
}

// TestStreamRendererStripsControls streams an answer full of control
// sequences in small chunks, on a terminal and off one: none reaches the
// output or Content, and the text around them does.
func TestStreamRendererStripsControls(t *testing.T) {
	answer := "Hi \x1b]52;c;cm0gLXJmIH4=\x07there\x1b[8m hidden\x1b[0m.\r\n\n\x1b]0;title\x1b\\Done \u009b2J."
	for _, tty := range []bool{true, false} {
		var out bytes.Buffer
		r := &StreamRenderer{out: &out, tty: tty, width: 40, height: 40, md: newMarkdown(40)}
		for i := 0; i < len(answer); i += 3 {
			r.Delta(docsgpt.Delta{Content: answer[i:min(i+3, len(answer))]})
			r.lastDraw = r.lastDraw.Add(-frameInterval)
		}
		r.Flush()
		if want := "Hi there hidden.\n\nDone ."; r.Content() != want {
			t.Errorf("tty %v: Content() = %q, want %q", tty, r.Content(), want)
		}
		got := out.String()
		if tty {
			got = emulate(t, got, 40, 40)
		}
		for _, bad := range []string{"\x1b]", "\x07", "\x1b[8m", "\u009b", "\r", "52;", "title"} {
			if strings.Contains(got, bad) {
				t.Errorf("tty %v: output has %q: %q", tty, bad, got)
			}
		}
		if !strings.Contains(got, "Hi there hidden.") || !strings.Contains(got, "Done .") {
			t.Errorf("tty %v: text lost: %q", tty, got)
		}
	}
}

var escape = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// emulate replays out on a terminal of the given size and returns the text
// left on it, scrollback included. It knows the sequences the renderer
// writes: cursor up (which cannot reach the scrollback), clear line, and
// styling.
func emulate(t *testing.T, out string, width, height int) string {
	t.Helper()
	var lines [][]rune
	row, col, bottom := 0, 0, 0
	put := func(c rune) {
		for len(lines) <= row {
			lines = append(lines, nil)
		}
		if col >= width {
			row, col = row+1, 0
			bottom = max(bottom, row)
			for len(lines) <= row {
				lines = append(lines, nil)
			}
		}
		for len(lines[row]) <= col {
			lines[row] = append(lines[row], ' ')
		}
		lines[row][col] = c
		col++
	}
	for len(out) > 0 {
		if loc := escape.FindStringIndex(out); loc != nil && loc[0] == 0 {
			seq := out[:loc[1]]
			out = out[loc[1]:]
			switch seq[len(seq)-1] {
			case 'A':
				n, _ := strconv.Atoi(seq[2 : len(seq)-1])
				if row -= n; row < bottom-height+1 || row < 0 {
					t.Fatalf("cursor moved up into the scrollback")
				}
			case 'K':
				if seq != "\x1b[2K" {
					t.Fatalf("unexpected sequence %q", seq)
				}
				if row < len(lines) {
					lines[row] = nil
				}
			}
			continue
		}
		c := []rune(out)[0]
		out = out[len(string(c)):]
		switch c {
		case '\n':
			row, col = row+1, 0
			bottom = max(bottom, row)
		case '\r':
			col = 0
		default:
			put(c)
		}
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(strings.TrimRight(string(l), " "))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func TestRewrap(t *testing.T) {
	in := "\x1b[38;5;8m• \x1b[0mfirst item that is long\n• short\n  1. nested item that wraps\nits hard break\n\n│ a quoted line that wraps\nplain paragraph that wraps"
	want := "• first item that is\n  long\n• short\n  1. nested item\n     that wraps\n     its hard break\n\n│ a quoted line that\n│ wraps\nplain paragraph that\nwraps"
	if got := xansi.Strip(rewrap(in, 20)); got != want {
		t.Errorf("rewrap:\n got %q\nwant %q", got, want)
	}
	if got := tidy("a\n\n\n\nb\n\n"); got != "a\n\nb" {
		t.Errorf("tidy kept doubled blank lines: %q", got)
	}
}

func TestWrapWords(t *testing.T) {
	for _, tt := range []struct {
		s     string
		width int
		want  string
	}{
		{"one two three", 7, "one two\nthree"},
		{"an id 6f0e1c2a-1111-2222 here", 16, "an id 6f0e1c2a-1\n111-2222 here"},
		{"see https://example.com/a/very/long/path ok", 20, "see https://example.\ncom/a/very/long/path\nok"},
		{"中文字符测试中文", 5, "中文\n字符\n测试\n中文"},
		{"中文", 1, "中\n文"},
		{"x \x1b[3mitalic words\x1b[0m y", 8, "x italic\nwords y"},
	} {
		got := wrapWords(tt.s, tt.width)
		if s := xansi.Strip(strings.Join(got, "\n")); s != tt.want {
			t.Errorf("wrapWords(%q, %d) = %q, want %q", tt.s, tt.width, s, tt.want)
		}
	}
	if got := wrapWords("x \x1b[3mitalic words\x1b[0m y", 8); !strings.HasPrefix(got[1], "\x1b[3mwords") {
		t.Errorf("the second row does not set its style: %q", got)
	}
}

// TestStreamRendererCRLF checks an answer with CRLF line ends renders like
// the LF one, even with each CR and LF in a chunk of its own.
func TestStreamRendererCRLF(t *testing.T) {
	render := func(text string, size int) string {
		var out bytes.Buffer
		r := &StreamRenderer{out: &out, tty: true, width: 40, height: 40, md: newMarkdown(40)}
		for i := 0; i < len(text); i += size {
			r.Delta(docsgpt.Delta{Content: text[i:min(i+size, len(text))]})
			r.lastDraw = r.lastDraw.Add(-frameInterval)
		}
		r.Flush()
		return emulate(t, out.String(), 40, 40)
	}
	want := render(sample, len(sample))
	for _, size := range []int{1, 5} {
		if got := render(strings.ReplaceAll(sample, "\n", "\r\n"), size); got != want {
			t.Errorf("CRLF in chunks of %d:\n--- got\n%s\n--- want\n%s", size, got, want)
		}
	}
}
