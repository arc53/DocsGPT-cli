package host

import (
	"fmt"
	"os"
	"path/filepath"

	"docsgpt-cli/internal/display"
)

// EnterServiceMode reroutes the daemon's output for running under a service
// manager with no terminal attached (the Windows scheduled task): stdout and
// stderr append to ``logFile``, styling is disabled so the log stays free of
// ANSI escapes, and any attached console window is dropped. Callers must
// invoke it before printing anything.
func EnterServiceMode(logFile string) error {
	if logFile == "" {
		logFile = DefaultHostConfig().LogFile
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0700); err != nil {
		return fmt.Errorf("mkdir log dir: %w", err)
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	os.Stdout = f
	os.Stderr = f
	display.UsePlainTheme()
	DetachConsole()
	return nil
}
