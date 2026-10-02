package display

import (
	"bytes"
	"strings"
	"testing"
)

func TestDiffPreview(t *testing.T) {
	UsePlainTheme()
	defer InitTheme("dark")
	before := "alpha\nbeta\ngamma\ndelta\n"
	after := "alpha\nBETA\ngamma\ndelta\nepsilon\n"
	preview, added, removed := DiffPreview(before, after, 10)
	want := []string{"2 - beta", "2 + BETA", "5 + epsilon"}
	if added != 2 || removed != 1 || strings.Join(preview, "|") != strings.Join(want, "|") {
		t.Errorf("got %q (+%d -%d), want %q (+2 -1)", preview, added, removed, want)
	}

	preview, added, _ = DiffPreview("", "a\nb\nc\n", 2)
	if added != 3 || len(preview) != 3 || preview[2] != "… 1 more changed line" {
		t.Errorf("new file: got %q (+%d)", preview, added)
	}
}

func TestTailView(t *testing.T) {
	UsePlainTheme()
	defer InitTheme("dark")
	var out bytes.Buffer
	v := &TailView{out: &out, width: 80}
	for i := 0; i < 8; i++ {
		v.Write([]byte("line\x1b[31m red\x1b[0m\tx\n"))
	}
	v.Write([]byte("progress 10%\rprogress 100%"))
	v.Close()
	want := "  … 4 earlier lines\n" + strings.Repeat("  line red    x\n", 4) + "  progress 100%\n"
	if out.String() != want {
		t.Errorf("got\n%q\nwant\n%q", out.String(), want)
	}
}
