package ui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// stubClipboard records what is copied instead of touching the system's.
func stubClipboard(t *testing.T) *[]string {
	var got []string
	old := systemClipboard
	systemClipboard = func(s string) error { got = append(got, s); return nil }
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_CLIENT", "")
	t.Cleanup(func() { systemClipboard = old })
	return &got
}

// TestOSC52: over SSH a copy goes to the terminal as an OSC 52 sequence,
// within its size limit.
func TestOSC52(t *testing.T) {
	copied := stubClipboard(t)
	t.Setenv("SSH_TTY", "/dev/pts/1")
	var out bytes.Buffer
	if err := Copy("héllo", &out); err != nil || len(*copied) != 0 {
		t.Fatalf("err %v, system clipboard %q", err, *copied)
	}
	if want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("héllo")) + "\a"; out.String() != want {
		t.Fatalf("%q, want %q", out.String(), want)
	}
	out.Reset()
	if err := Copy(strings.Repeat("x", 80_000), &out); err == nil || out.Len() != 0 {
		t.Fatalf("too long: err %v, wrote %d bytes", err, out.Len())
	}

	t.Setenv("SSH_TTY", "")
	systemClipboard = func(string) error { return errors.New("no xclip") }
	if err := Copy("x", &out); err != nil || out.String() != "\x1b]52;c;eA==\a" {
		t.Fatalf("no system clipboard: err %v, %q", err, out.String())
	}
}
