package tools

import (
	"context"
	"strings"
	"testing"
)

// TestRunShellStatus: RunShell reports whether the command succeeded, so
// a failing !cmd is not shown as done.
func TestRunShellStatus(t *testing.T) {
	if out, ok := RunShell(context.Background(), choosingUI{}, "echo hi"); !ok || !strings.Contains(out, "hi") {
		t.Errorf("echo hi: %q, ok %v", out, ok)
	}
	if out, ok := RunShell(context.Background(), choosingUI{}, "exit 3"); ok || !strings.Contains(out, "code 3") {
		t.Errorf("exit 3: %q, ok %v", out, ok)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := RunShell(ctx, choosingUI{}, "echo hi"); ok {
		t.Error("a cancelled command succeeded")
	}
}
