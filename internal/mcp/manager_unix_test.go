//go:build !windows

package mcp_test

import (
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/mcp/mcptest"
)

// TestCloseKillsTheGroup: what a server started dies with it.
func TestCloseKillsTheGroup(t *testing.T) {
	m := start(t, mcptest.Server("test"))
	out, err := call(t, m, "mcp__test__spawn", nil)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(out)
	if err != nil {
		t.Fatal(out)
	}
	m.Close()
	time.Sleep(100 * time.Millisecond)
	if syscall.Kill(pid, 0) == nil {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("child %d survived", pid)
	}
	if _, err := call(t, m, "mcp__test__echo", nil); err == nil {
		t.Error("a call after Close worked")
	}
	if fi, err := os.Stat(m.Status()[0].Log); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("log: %v", err)
	}
}
