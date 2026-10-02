package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlwaysKey(t *testing.T) {
	for cmd, want := range map[string]string{
		"git status":            "git",
		"  ls -la src ":         "ls",
		"git log | head":        "",
		"make && make install":  "",
		"echo hi > out.txt":     "",
		"cat < in.txt":          "",
		"echo $(whoami)":        "",
		"echo `whoami`":         "",
		"echo ${HOME}":          "",
		"ls; rm -rf x":          "",
		"git status\nrm -rf x":  "",
		"sleep 5 &":             "",
		"sudo ls":               "",
		"bash -c 'ls'":          "",
		"env FOO=1 ls":          "",
		"":                      "",
		"grep shutdown app.log": "grep",
	} {
		if got := alwaysKey(cmd); got != want {
			t.Errorf("alwaysKey(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestTruncateTail(t *testing.T) {
	if got := truncateTail("a\nb\n", 0); got != "a\nb\n" {
		t.Errorf("short output changed: %q", got)
	}

	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	got := truncateTail(b.String(), 0)
	if !strings.HasPrefix(got, "[Output truncated: showing the last 2000 of 3000 lines.]\n1001\n") || !strings.HasSuffix(got, "3000\n") {
		t.Errorf("line cap: got %q…", got[:80])
	}

	got = truncateTail("x\n", 7)
	if got != "[Output truncated: showing the last 1 of 8 lines.]\nx\n" {
		t.Errorf("dropped lines not counted: %q", got)
	}

	long := strings.Repeat("é", maxOutputBytes) // 2 bytes a rune
	got = truncateTail(long, 0)
	body := got[strings.IndexByte(got, '\n')+1:]
	if !strings.HasPrefix(got, "[Output truncated: showing the last") || len(body) > maxOutputBytes || strings.Trim(body, "é") != "" {
		t.Errorf("one long line: not cut at a rune start (%d bytes)", len(body))
	}
}

func TestTailBuffer(t *testing.T) {
	var buf tailBuffer
	for i := 1; i <= 100000; i++ {
		fmt.Fprintf(&buf, "line %d\n", i)
	}
	if len(buf.buf) > 4*maxOutputBytes {
		t.Errorf("buffer kept %d bytes", len(buf.buf))
	}
	got := buf.String()
	if !strings.HasPrefix(got, "[Output truncated: showing the last 2000 of 100000 lines.]\nline 98001\n") {
		t.Errorf("got %q…", got[:80])
	}
}

func TestReadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	var b strings.Builder
	for i := 1; i <= 2500; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	os.WriteFile(path, []byte(b.String()), 0o644)

	text, shown, total, err := readFile(path, 0, 0)
	if err != nil || shown != 2000 || total != 2500 || !strings.HasSuffix(text, "2000\n\n[Showing lines 1-2000 of 2500. Use offset=2001 to continue.]") {
		t.Errorf("default read: shown %d of %d, err %v, tail %q", shown, total, err, text[len(text)-70:])
	}
	text, shown, _, _ = readFile(path, 2001, 0)
	if shown != 500 || !strings.HasPrefix(text, "2001\n") || strings.Contains(text, "[Showing") {
		t.Errorf("offset read: shown %d, text %q…", shown, text[:20])
	}
	text, shown, _, _ = readFile(path, 10, 3)
	if shown != 3 || !strings.HasPrefix(text, "10\n11\n12\n\n[Showing lines 10-12 of 2500. Use offset=13 to continue.]") {
		t.Errorf("limit read: %q", text)
	}
	if _, _, _, err := readFile(path, 3000, 0); err == nil {
		t.Error("offset past the end: want an error")
	}
}
