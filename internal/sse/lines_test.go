package sse

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, r *LineReader) []Line {
	t.Helper()
	var out []Line
	for {
		line, err := r.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, line)
	}
}

func TestLinesOfAnyLengthUnderTheLimitAreReadWhole(t *testing.T) {
	big := strings.Repeat("z", 5<<20) // 5 MiB: far past bufio's own buffer
	input := "short\n" + big + "\n\nlast"
	lines := readAll(t, NewLineReader(strings.NewReader(input), DefaultMaxLine))

	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if string(lines[0].Text) != "short" || string(lines[1].Text) != big || len(lines[2].Text) != 0 {
		t.Errorf("lines read wrong: %q / %d bytes / %q", lines[0].Text, len(lines[1].Text), lines[2].Text)
	}
	if lines[1].Oversized || lines[1].Size != len(big) {
		t.Errorf("big line: oversized=%v size=%d", lines[1].Oversized, lines[1].Size)
	}
	if string(lines[3].Text) != "last" {
		t.Errorf("an unterminated last line should still be read, got %q", lines[3].Text)
	}
}

func TestCarriageReturnsAreTrimmed(t *testing.T) {
	lines := readAll(t, NewLineReader(strings.NewReader("a\r\nb\r\n"), DefaultMaxLine))
	if len(lines) != 2 || string(lines[0].Text) != "a" || string(lines[1].Text) != "b" {
		t.Errorf("lines = %+v", lines)
	}
}

func TestALineOverTheLimitIsSkippedKeepingItsStart(t *testing.T) {
	big := `data: {"type":"workflow_step","state_delta":"` + strings.Repeat("x", 3<<20) + `"}`
	input := "before\n" + big + "\nafter\n"
	lines := readAll(t, NewLineReader(strings.NewReader(input), 1<<20))

	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	skipped := lines[1]
	if !skipped.Oversized {
		t.Fatal("the long line should be marked oversized")
	}
	if skipped.Size != len(big) {
		t.Errorf("Size = %d, want %d", skipped.Size, len(big))
	}
	if len(skipped.Text) != PrefixBytes || !bytes.HasPrefix([]byte(big), skipped.Text) {
		t.Errorf("an oversized line keeps its first %d bytes, got %d", PrefixBytes, len(skipped.Text))
	}
	if string(lines[2].Text) != "after" {
		t.Errorf("reading continues after a skipped line, got %q", lines[2].Text)
	}
}

func TestReadErrorsAreReturned(t *testing.T) {
	r := NewLineReader(io.MultiReader(strings.NewReader("partial"), errReader{}), DefaultMaxLine)
	if _, err := r.Next(); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("want the read error, got %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestFrameType(t *testing.T) {
	cases := map[string]string{
		`data: {"type":"workflow_step","node_id":"n"`: "workflow_step",
		`data: {"type": "answer", "answer": "x`:       "answer",
		`data:{"node_id":"n","type":"workflow_step"`:  "workflow_step",
		`data: {"answer":"no type key here`:           "",
		`id: 12`:                                      "",
	}
	for prefix, want := range cases {
		if got := FrameType([]byte(prefix)); got != want {
			t.Errorf("FrameType(%q) = %q, want %q", prefix, got, want)
		}
	}
}
