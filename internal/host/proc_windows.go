package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// shellCommand runs command with cmd.exe, given the command line as it is:
// Go would quote command as one argument, escaping its quotes as \", which
// cmd.exe does not read. /S /C "…" makes cmd.exe take off the outer quotes
// alone; /D skips the AutoRun commands of the registry.
func shellCommand(command string) *exec.Cmd {
	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /D /S /C "` + command + `"`}
	return cmd
}

// newGroup is a no-op on Windows: stopGroup ends the whole process tree.
func newGroup(*exec.Cmd) {}

// stopGroup ends the command's process tree. Windows has no SIGTERM for a
// console program, so both the polite and the forceful stop are
// `taskkill /T /F`, falling back to killing the shell alone.
func stopGroup(cmd *exec.Cmd, _ bool) {
	if exec.Command(taskkill(), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run() != nil {
		_ = cmd.Process.Kill()
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

// lockFile takes an exclusive lock on f without waiting, so two daemons
// never share one spool. The lock ends with the process.
func lockFile(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
}
