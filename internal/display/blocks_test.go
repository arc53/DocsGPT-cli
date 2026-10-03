package display

import (
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
	want := "$ make␛[2J in /src\n  … 4 earlier lines\n  line 4\n  line 5\n  line 6\n  line 7\n  progress 100%\n  ✗ exit 2 · 0.1s"
	if got := xansi.Strip(strings.Join(b.Lines(40), "\n")); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFit(t *testing.T) {
	got := fit([]string{"a\nb", "    " + strings.Repeat("x", 12)}, 10)
	want := []string{"a", "b", "    xxxxxx", "    xxxxxx"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("fit: %q", got)
	}
}
