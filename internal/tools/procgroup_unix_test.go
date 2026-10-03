//go:build !windows

package tools

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunCommandCancelKillsChildren cancels a command whose shell has a
// child of its own: both must die, and Execute must return promptly.
func TestRunCommandCancelKillsChildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	err := runCommand(ctx, "sleep 30 & echo $! > "+pidFile+"; wait", "", time.Minute, io.Discard)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("runCommand returned after %v, want right after the cancel", took)
	}
	if err != context.Canceled {
		t.Errorf("err = %v, want the cancellation", err)
	}

	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("child process %d survived the cancel", pid)
	}
}

func TestRunCommandTimeout(t *testing.T) {
	start := time.Now()
	err := runCommand(context.Background(), "sleep 30; echo late", "", 300*time.Millisecond, io.Discard)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("runCommand returned after %v, want right after the timeout", took)
	}
	if err != errTimeout {
		t.Errorf("err = %v, want the timeout", err)
	}
}

// TestRunCommandHasNoTerminal: a command cannot read the user's terminal,
// so a prompt for a password fails at once instead of waiting.
func TestRunCommandHasNoTerminal(t *testing.T) {
	var out strings.Builder
	start := time.Now()
	err := runCommand(context.Background(), "read line </dev/tty && echo got-tty; read line; echo stdin=$?", "", 10*time.Second, &out)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("took %v, want an immediate failure", took)
	}
	if err != nil || strings.Contains(out.String(), "got-tty") || !strings.Contains(out.String(), "stdin=1") {
		t.Errorf("err %v, output %q: want /dev/tty unavailable and stdin empty", err, out.String())
	}
}
