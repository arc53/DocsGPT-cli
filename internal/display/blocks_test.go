package display

import (
	"fmt"
	"strings"
	"testing"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	xansi "github.com/charmbracelet/x/ansi"
)

// TestAnswerStreams: however the answer arrives, the lines end the same,
// none wider than the width, and a new width renders it all again.
func TestAnswerStreams(t *testing.T) {
	var want string
	for _, size := range []int{len(sample), 1, 3, 7} {
		a := NewAnswer(false)
		for i := 0; i < len(sample); i += size {
			a.Delta(docsgpt.Delta{Content: sample[i:min(i+size, len(sample))]})
			a.Lines(40)
		}
		a.Finish()
		lines := a.Lines(40)
		for _, l := range lines {
			if w := xansi.StringWidth(l); w > 40 {
				t.Fatalf("size %d: a line of %d columns: %q", size, w, l)
			}
		}
		got := xansi.Strip(strings.Join(lines, "\n"))
		if size == len(sample) {
			want = got
		} else if got != want {
			t.Errorf("size %d:\n%s\nwant\n%s", size, got, want)
		}
		if wide := xansi.Strip(strings.Join(a.Lines(80), "\n")); !strings.Contains(wide, "first item that is long enough to wrap around the narrow terminal") {
			t.Errorf("not wrapped again at 80:\n%s", wide)
		}
	}
	if !strings.Contains(want, "curl -fsSL") || strings.Count(want, "Install") != 1 {
		t.Fatalf("answer:\n%s", want)
	}
}

// TestAnswerReasoning: the reasoning shows above the answer only when
// asked for, and the answer loses its control sequences.
func TestAnswerReasoning(t *testing.T) {
	for _, show := range []bool{false, true} {
		a := NewAnswer(show)
		visible := a.Delta(docsgpt.Delta{ReasoningContent: "hmm"})
		a.Delta(docsgpt.Delta{Content: "ok \x1b]52;c;bad\x07done"})
		a.Finish()
		got := xansi.Strip(strings.Join(a.Lines(40), "\n"))
		if visible != show || strings.Contains(got, "hmm") != show || !strings.HasSuffix(got, "ok done") {
			t.Errorf("show %v: visible %v, %q", show, visible, got)
		}
	}
}

func TestToolBlock(t *testing.T) {
	UsePlainTheme()
	defer InitTheme("dark")
	b := NewToolBlock("$ make\x1b[2J", "in /src")
	for i := range 8 {
		b.Write([]byte("line " + string(rune('0'+i)) + "\n"))
	}
	b.Write([]byte("progress 10%\rprogress 100%"))
	b.Close(false, "exit 2 · 0.1s")
	want := "$ make␛[2J in /src\n  … 4 earlier lines · ctrl+o to expand\n  line 4\n  line 5\n  line 6\n  line 7\n  progress 100%\n  ✗ exit 2 · 0.1s"
	if got := xansi.Strip(strings.Join(b.Lines(40), "\n")); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if !b.Folds() {
		t.Error("9 lines do not fold")
	}

	b.Expand(true)
	want = "$ make␛[2J in /src\n  line 0\n  line 1\n  line 2\n  line 3\n  line 4\n  line 5\n  line 6\n  line 7\n  progress 100%\n  ✗ exit 2 · 0.1s · ctrl+o to collapse"
	if got := xansi.Strip(strings.Join(b.Lines(40), "\n")); got != want {
		t.Errorf("expanded: got\n%s\nwant\n%s", got, want)
	}
	if got, want := copied(b, 40), strings.ReplaceAll(want, "\n  ", "\n"); got != want {
		t.Errorf("expanded copies:\n%s\nwant\n%s", got, want)
	}
	b.Expand(false)
	if got := xansi.Strip(strings.Join(b.Lines(40), "\n")); !strings.Contains(got, "… 4 earlier lines") {
		t.Errorf("collapsed again:\n%s", got)
	}

	short := NewToolBlock("$ true", "")
	short.Write([]byte("a\nb\n"))
	short.Close(true, "exit 0")
	short.Expand(true)
	if got := xansi.Strip(strings.Join(short.Lines(40), "\n")); short.Folds() || got != "$ true\n  a\n  b\n  ✓ exit 0" {
		t.Errorf("short output: %q", got)
	}
}

// TestToolBlockKeeps: expanded, a block shows what the model gets of the
// output, the last 2000 lines or 50 KB, and counts the rest.
func TestToolBlockKeeps(t *testing.T) {
	UsePlainTheme()
	defer InitTheme("dark")
	b := NewToolBlock("$ seq", "")
	b.Expand(true)
	for i := range 2500 {
		fmt.Fprintf(b, "%d\n", i)
	}
	rows := strings.Split(xansi.Strip(strings.Join(b.Lines(40), "\n")), "\n")
	if len(rows) != 2002 || rows[1] != "  … 500 earlier lines" || rows[2] != "  500" || rows[2001] != "  2499" {
		t.Errorf("by lines: %d rows, %q … %q", len(rows), rows[1:3], rows[len(rows)-1])
	}

	b = NewToolBlock("$ big", "")
	b.Expand(true)
	line := strings.Repeat("x", 99)
	for range 1000 {
		b.Write([]byte(line + "\n"))
	}
	b.Write([]byte(strings.Repeat("y", 10000) + "\n"))
	if n := len(b.out); b.size > keptBytes || b.size < keptBytes-200 || len(b.out[n-1]) != lineBytes {
		t.Errorf("by bytes: %d lines, %d bytes, last %d", n, b.size, len(b.out[n-1]))
	}
}

func TestFit(t *testing.T) {
	got := fit([]string{"a\nb", "    " + strings.Repeat("x", 12)}, 10)
	want := []string{"a", "b", "    xxxxxx", "    xxxxxx"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("fit: %q", got)
	}
}
