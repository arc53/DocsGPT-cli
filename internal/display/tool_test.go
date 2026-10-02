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

func TestToolBox(t *testing.T) {
	UsePlainTheme()
	defer InitTheme("dark")
	b := &toolBox{bg: "<bg>", width: 12}
	if got, want := b.row(b.bg, "a\x1b[0mb"), "<bg> a\x1b[0m<bg>b"+strings.Repeat(" ", 9)+"\x1b[0m"; got != want {
		t.Errorf("row: got %q, want %q", got, want)
	}
	if got := b.row(b.bg, strings.Repeat("x", 20)); !strings.HasPrefix(got, "<bg> "+strings.Repeat("x", 9)+"… \x1b[0m") {
		t.Errorf("long row not cut to the width: %q", got)
	}

	// The live region ends with the bottom padding until Close, which
	// leaves the output as rows of the block.
	var out bytes.Buffer
	v := &TailView{out: &out, tty: true, width: 12, box: b}
	v.draw()
	v.tail, v.total = []string{"one"}, 1
	v.draw()
	out.Reset()
	v.Close()
	want := "\x1b[?2026h\r\x1b[2K\x1b[1A\x1b[2K\x1b[1A\x1b[2K" + b.row(b.bg, "one") + "\n\x1b[?2026l"
	if out.String() != want || strings.Join(b.rows, "|") != "one" {
		t.Errorf("close: got %q (rows %q), want %q", out.String(), b.rows, want)
	}
}
