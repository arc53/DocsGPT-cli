package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// The DocsGPT server renames tools ("write_file" → "write_file_ct0").
var toolSuffixRe = regexp.MustCompile(`_ct\d+$`)

// NormalizeName strips the suffix the server appends to tool names.
func NormalizeName(name string) string {
	return toolSuffixRe.ReplaceAllString(name, "")
}

var errTimeout = errors.New("timed out")

// runCommand runs command through the shell, copying its combined output
// to out. It gets at most timeout (errTimeout) and is killed when ctx is
// cancelled (context.Canceled). A non-zero exit is an *exec.ExitError.
func runCommand(ctx context.Context, command, dir string, timeout time.Duration, out io.Writer) error {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(runCtx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(runCtx, "sh", "-c", command)
	}
	killProcessGroup(cmd)
	// Don't wait forever for output from a process that escaped the kill.
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = out, out

	err := cmd.Run()
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return context.Canceled
	case runCtx.Err() == context.DeadlineExceeded:
		return errTimeout
	}
	return err
}

// readFile returns lines [offset, offset+limit) of the file at path (1-based;
// limit 0 reads to the end), within maxOutputLines and maxOutputBytes, with
// a note telling the model how to continue when lines are left. shown and
// total count the lines returned and in the file.
func readFile(path string, offset, limit int) (text string, shown, total int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, 0, err
	}
	if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return "", 0, 0, fmt.Errorf("%s is a binary file", path)
	}
	lines := splitLines(string(data))
	total = len(lines)
	start := max(offset, 1) - 1
	if start >= total && total > 0 {
		return "", 0, total, fmt.Errorf("offset %d is beyond the end of the file (%d lines)", offset, total)
	}
	end := total
	if limit > 0 {
		end = min(end, start+limit)
	}
	end = min(end, start+maxOutputLines)

	var b strings.Builder
	n := start
	for ; n < end; n++ {
		line := lines[n]
		if b.Len()+len(line)+1 > maxOutputBytes {
			if n > start {
				break
			}
			cut := maxOutputBytes
			for cut > 0 && cut < len(line) && !utf8.RuneStart(line[cut]) {
				cut--
			}
			line = line[:cut] + " … [line truncated]"
		}
		b.WriteString(line + "\n")
	}
	if n < total {
		fmt.Fprintf(&b, "\n[Showing lines %d-%d of %d. Use offset=%d to continue.]", start+1, n, total, n+1)
	}
	return b.String(), n - start, total, nil
}

// writeFile writes content to path, creating missing parent directories.
func writeFile(path, content string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// splitLines splits s into lines; a final newline ends the last line
// rather than starting an empty one.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
