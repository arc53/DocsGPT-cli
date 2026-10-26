package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// ownGroup gives cmd a hidden console of its own, so a server cannot read
// the user's keys or open a window.
func ownGroup(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}

// terminate ends the process tree at once: Windows has no SIGTERM, and
// once the process is gone its children can no longer be found from it.
func terminate(cmd *exec.Cmd) { kill(cmd) }

func kill(cmd *exec.Cmd) {
	if exec.Command(taskkill(), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run() != nil {
		cmd.Process.Kill()
	}
}

// killOrphans does nothing: a tree that lost its root cannot be found.
func killOrphans(*exec.Cmd) {}

// taskkill is the system's taskkill.exe, by its full path.
func taskkill() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}
