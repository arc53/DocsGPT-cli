package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// shellCommand runs command with cmd.exe, given the command line as it is:
// Go would quote command as one argument, escaping its quotes as \", which
// cmd.exe does not read (echo "a b" printed \"a b\", a quoted program path
// was not found). /S /C "…" makes cmd.exe take off the outer quotes alone;
// /D skips the AutoRun commands of the registry.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /D /S /C "` + command + `"`}
	// cmd.exe looks for a program in the working directory before PATH,
	// so an approved "git status" could run a repository's git.bat.
	cmd.Env = append(os.Environ(), "NoDefaultCurrentDirectoryInExePath=1")
	return cmd
}

// killProcessGroup gives cmd a hidden console of its own, so a program that
// reads the console directly (a password prompt) cannot take the user's
// keystrokes, and makes cancellation kill the whole process tree.
func killProcessGroup(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	cmd.Cancel = func() error {
		if exec.Command(taskkill(), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run() == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}

// taskkill is the system's taskkill.exe, by its full path: one found on
// PATH (or in the working directory) could be anything.
func taskkill() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}
