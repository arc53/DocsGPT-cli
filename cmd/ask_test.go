package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadPipedStdin(t *testing.T) {
	stdinHint = 50 * time.Millisecond
	t.Cleanup(func() { stdinHint = time.Second })
	path := filepath.Join(t.TempDir(), "in")
	os.WriteFile(path, []byte("line one\nline two\n"), 0o644)

	f, _ := os.Open(path)
	if got, _ := readPipedStdin(f, nil); got != "line one\nline two" {
		t.Errorf("file = %q", got)
	}
	f.Seek(9, 0) // read where the file is, like a shell would
	if got, _ := readPipedStdin(f, nil); got != "line two" {
		t.Errorf("file read mid-way = %q", got)
	}
	f.Close()

	if null, err := os.Open(os.DevNull); err == nil {
		if got, _ := readPipedStdin(null, nil); got != "" {
			t.Errorf("/dev/null = %q", got)
		}
		null.Close()
	}

	// A slow producer is waited for, with a hint that is cleared again.
	r, w, _ := os.Pipe()
	go func(w *os.File) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("late "))
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("output\n"))
		w.Close()
	}(w)
	var hint bytes.Buffer
	if got, _ := readPipedStdin(r, &hint); got != "late output" {
		t.Errorf("slow pipe = %q", got)
	}
	if h := hint.String(); !strings.Contains(h, "--no-stdin") || !strings.HasSuffix(h, "\r\x1b[2K") {
		t.Errorf("hint = %q", h)
	}
	r.Close()

	r, w, _ = os.Pipe()
	w.Write([]byte("quick"))
	w.Close()
	hint.Reset()
	if got, _ := readPipedStdin(r, &hint); got != "quick" || hint.Len() != 0 {
		t.Errorf("quick pipe = %q, hint %q", got, hint.String())
	}
	r.Close()

	r, w, _ = os.Pipe()
	go func(w *os.File) {
		w.Write([]byte(strings.Repeat("x", maxStdin+10)))
		w.Close()
	}(w)
	got, _ := readPipedStdin(r, nil)
	if !strings.HasPrefix(got, "xxx") || !strings.HasSuffix(got, "[… truncated at 1 MB]") || len(got) > maxStdin+40 {
		t.Errorf("large pipe: %d bytes, ends %q", len(got), got[len(got)-30:])
	}
}
