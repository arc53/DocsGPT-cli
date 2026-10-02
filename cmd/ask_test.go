package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadPipedStdin(t *testing.T) {
	stdinWait = 100 * time.Millisecond
	path := filepath.Join(t.TempDir(), "in")
	os.WriteFile(path, []byte("line one\nline two\n"), 0o644)

	f, _ := os.Open(path)
	if got, _ := readPipedStdin(f, true); got != "line one\nline two" {
		t.Errorf("file from its start = %q", got)
	}
	f.Seek(9, 0) // a "while read" loop took the first line
	if got, _ := readPipedStdin(f, true); got != "" {
		t.Errorf("file read mid-way with a question = %q", got)
	}
	f.Seek(9, 0)
	if got, _ := readPipedStdin(f, false); got != "line two" {
		t.Errorf("file read mid-way without a question = %q", got)
	}
	f.Close()

	r, w, _ := os.Pipe()
	start := time.Now()
	if got, _ := readPipedStdin(r, true); got != "" || time.Since(start) > time.Second {
		t.Errorf("idle pipe = %q after %s", got, time.Since(start))
	}
	w.Close()
	r.Close()

	r, w, _ = os.Pipe()
	go func() {
		w.Write([]byte(strings.Repeat("x", maxStdin+10)))
		w.Close()
	}()
	got, _ := readPipedStdin(r, true)
	if !strings.HasPrefix(got, "xxx") || !strings.HasSuffix(got, "[… truncated at 1 MB]") || len(got) > maxStdin+40 {
		t.Errorf("large pipe: %d bytes, ends %q", len(got), got[len(got)-30:])
	}
}
