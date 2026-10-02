//go:build !windows

package tools

import (
	"context"
	"encoding/json"
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
	args, _ := json.Marshal(map[string]string{
		"command": "sleep 30 & echo $! > " + pidFile + "; wait",
	})

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	res := Execute(ctx, "run_command", string(args), time.Minute)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Execute returned after %v, want right after the cancel", took)
	}
	if res.Error != "command interrupted by the user" {
		t.Errorf("Error = %q, want the interruption", res.Error)
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
	args, _ := json.Marshal(map[string]string{"command": "sleep 30; echo late"})
	start := time.Now()
	res := Execute(context.Background(), "run_command", string(args), 300*time.Millisecond)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Execute returned after %v, want right after the timeout", took)
	}
	if res.Error != "command timed out" {
		t.Errorf("Error = %q, want the timeout", res.Error)
	}
}
