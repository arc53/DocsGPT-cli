package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
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

	ctx := context.Background()
	text, shown, total, err := readFile(ctx, path, 0, 0)
	if err != nil || shown != 2000 || total != 2500 || !strings.HasSuffix(text, "2000\n\n[Showing lines 1-2000 of 2500. Use offset=2001 to continue.]") {
		t.Errorf("default read: shown %d of %d, err %v, tail %q", shown, total, err, text[len(text)-70:])
	}
	text, shown, _, _ = readFile(ctx, path, 2001, 0)
	if shown != 500 || !strings.HasPrefix(text, "2001\n") || strings.Contains(text, "[Showing") {
		t.Errorf("offset read: shown %d, text %q…", shown, text[:20])
	}
	text, shown, _, _ = readFile(ctx, path, 10, 3)
	if shown != 3 || !strings.HasPrefix(text, "10\n11\n12\n\n[Showing lines 10-12 of 2500. Use offset=13 to continue.]") {
		t.Errorf("limit read: %q", text)
	}
	if _, _, _, err := readFile(ctx, path, 3000, 0); err == nil {
		t.Error("offset past the end: want an error")
	}

	os.WriteFile(path, []byte("a\nb"), 0o644) // no final newline
	if text, shown, total, _ := readFile(ctx, path, 0, 0); text != "a\nb\n" || shown != 2 || total != 2 {
		t.Errorf("unterminated last line: %q %d/%d", text, shown, total)
	}
	os.WriteFile(path, nil, 0o644)
	if text, shown, total, err := readFile(ctx, path, 0, 0); text != "" || shown != 0 || total != 0 || err != nil {
		t.Errorf("empty file: %q %d/%d %v", text, shown, total, err)
	}

	// One line far over the budget is cut, and the next lines still counted.
	os.WriteFile(path, []byte(strings.Repeat("é", maxOutputBytes)+"\nnext\n"), 0o644)
	text, shown, total, err = readFile(ctx, path, 0, 0)
	if err != nil || shown != 1 || total != 2 || !strings.Contains(text, " … [line truncated]\n") || len(text) > maxOutputBytes+200 {
		t.Errorf("long line: shown %d of %d, %d bytes, err %v", shown, total, len(text), err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := readFile(cancelled, path, 0, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled read: err = %v", err)
	}
}

func TestRegularFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := regularFile(dir); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("directory: err = %v", err)
	}
	if _, err := regularFile(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file: want an error")
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, dev := range []string{"/dev/zero", "/dev/null", "/dev/tty"} {
		if _, err := regularFile(dev); err == nil {
			t.Errorf("%s: want it refused", dev)
		}
	}
	file := filepath.Join(dir, "f.txt")
	os.WriteFile(file, []byte("x\n"), 0o644)
	link := filepath.Join(dir, "link")
	os.Symlink("/dev/zero", link)
	if _, err := regularFile(link); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("symlink to a device: err = %v", err)
	}
}

func TestReadReason(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	proj := filepath.Join(home, "proj")
	os.MkdirAll(filepath.Join(proj, "sub", ".ssh"), 0o755)
	for _, f := range []string{"main.go", ".env", ".env.local", "prod.env", "server.pem", "id_ed25519", "id_test.go", "sub/.ssh/config", "notes.txt"} {
		os.WriteFile(filepath.Join(proj, f), []byte("x\n"), 0o644)
	}
	os.WriteFile(filepath.Join(home, "secret.txt"), []byte("x\n"), 0o644)
	os.Symlink(filepath.Join(home, "secret.txt"), filepath.Join(proj, "link.txt"))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(proj)

	reason := func(path string) string {
		real, err := regularFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return readReason(real)
	}
	for path, want := range map[string]string{
		"main.go":                         "",
		"id_test.go":                      "",
		"notes.txt":                       "",
		"./sub/../main.go":                "",
		".env":                            "may hold secrets",
		".env.local":                      "may hold secrets",
		"prod.env":                        "may hold secrets",
		"server.pem":                      "may hold secrets",
		"id_ed25519":                      "may hold secrets",
		"sub/.ssh/config":                 "may hold secrets",
		"../secret.txt":                   "outside the working directory",
		"link.txt":                        "outside the working directory",
		filepath.Join(home, "secret.txt"): "outside the working directory",
	} {
		if got := reason(path); got != want {
			t.Errorf("readReason(%s) = %q, want %q", path, got, want)
		}
	}

	t.Chdir(home) // the home directory is not a project: everything asks
	if got := reason("proj/main.go"); got == "" {
		t.Error("read under a home working directory: want approval")
	}
}

// TestSessionFileCalls runs read_file and write_file calls without a
// terminal: a read that needs approval cannot get it, so nothing is read.
func TestSessionFileCalls(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("the secret\n"), 0o644)
	proj := t.TempDir()
	os.WriteFile(filepath.Join(proj, "notes.txt"), []byte("hello\n"), 0o644)
	t.Chdir(proj)

	s := &Session{Timeout: time.Minute}
	call := func(name, args string) string {
		return s.Handle(context.Background(), func() {}, docsgpt.ToolCall{Function: docsgpt.FunctionCall{Name: name, Arguments: args}})
	}
	if got := call("read_file", `{"path":"notes.txt"}`); got != "hello\n" {
		t.Errorf("read in the working directory: %q", got)
	}
	if got := call("read_file", `{"path":"`+outside+`"}`); strings.Contains(got, "the secret") || !strings.Contains(got, "could not be approved") {
		t.Errorf("read outside the working directory without approval: %q", got)
	}
	if got := call("write_file", `{"path":"`+proj+`","content":"x"}`); !strings.Contains(got, "not a regular file") {
		t.Errorf("write to a directory: %q", got)
	}
	if runtime.GOOS != "windows" {
		if got := call("read_file", `{"path":"/dev/zero"}`); !strings.Contains(got, "not a regular file") {
			t.Errorf("read /dev/zero: %q", got)
		}
	}
}
