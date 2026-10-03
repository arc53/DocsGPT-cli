package tools

import (
	"bufio"
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
		// cmd.exe looks for a program in the working directory before PATH,
		// so an approved "git status" could run a repository's git.bat.
		cmd.Env = append(os.Environ(), "NoDefaultCurrentDirectoryInExePath=1")
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

// regularFile resolves path (relative to the working directory, symlinks
// followed) to the absolute path of a regular file. Devices, FIFOs and
// directories are refused: reading /dev/zero never ends, /dev/tty waits for
// the user.
func regularFile(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(real)
	switch {
	case err != nil:
		return "", err
	case fi.IsDir():
		return "", fmt.Errorf("%s is a directory", path)
	case !fi.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	return real, nil
}

// readFile returns lines [offset, offset+limit) of the regular file at path
// (1-based; limit 0 reads to the end), within maxOutputLines and
// maxOutputBytes, with a note telling the model how to continue when lines
// are left. shown and total count the lines returned and in the file. It
// reads in chunks, holding no more than it returns, and stops when ctx ends.
func readFile(ctx context.Context, path string, offset, limit int) (text string, shown, total int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return "", 0, 0, fmt.Errorf("%s is not a regular file", path)
	}
	r := bufio.NewReaderSize(f, 64*1024)
	if head, _ := r.Peek(8192); bytes.IndexByte(head, 0) >= 0 {
		return "", 0, 0, fmt.Errorf("%s is a binary file", path)
	}

	start := max(offset, 1) - 1
	want := maxOutputLines
	if limit > 0 {
		want = min(want, limit)
	}
	var (
		b       strings.Builder
		line    []byte // the current line, up to maxOutputBytes+1 bytes of it
		pending bool   // part of the current line was read
		full    bool   // no further line fits
	)
	for !full {
		if err := ctx.Err(); err != nil {
			return "", 0, 0, err
		}
		chunk, rerr := r.ReadSlice('\n')
		if rerr != nil && rerr != bufio.ErrBufferFull && rerr != io.EOF {
			return "", 0, 0, rerr
		}
		if total >= start {
			line = append(line, chunk[:min(len(chunk), maxOutputBytes+1-len(line))]...)
		}
		pending = pending || len(chunk) > 0
		if rerr == nil || rerr == io.EOF && pending {
			if total >= start {
				s := strings.TrimSuffix(string(line), "\n")
				switch {
				case shown == want:
					full = true
				case b.Len()+len(s)+1 <= maxOutputBytes:
					b.WriteString(s + "\n")
					shown++
				case shown > 0:
					full = true
				default: // a single line over the budget: keep its start
					cut := maxOutputBytes
					for cut > 0 && cut < len(s) && !utf8.RuneStart(s[cut]) {
						cut--
					}
					b.WriteString(s[:min(cut, len(s))] + " … [line truncated]\n")
					shown++
				}
			}
			total++
			line, pending = line[:0], false
		}
		if rerr == io.EOF {
			break
		}
	}
	if full { // only the line count is left
		buf, last := make([]byte, 64*1024), byte('\n')
		for {
			if err := ctx.Err(); err != nil {
				return "", 0, 0, err
			}
			n, rerr := r.Read(buf)
			if n > 0 {
				total += bytes.Count(buf[:n], []byte{'\n'})
				last = buf[n-1]
			}
			if rerr == io.EOF {
				break
			} else if rerr != nil {
				return "", 0, 0, rerr
			}
		}
		if last != '\n' {
			total++
		}
	}

	if start >= total && total > 0 {
		return "", 0, total, fmt.Errorf("offset %d is beyond the end of the file (%d lines)", offset, total)
	}
	if n := start + shown; n < total {
		fmt.Fprintf(&b, "\n[Showing lines %d-%d of %d. Use offset=%d to continue.]", start+1, n, total, n+1)
	}
	return b.String(), shown, total, nil
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
