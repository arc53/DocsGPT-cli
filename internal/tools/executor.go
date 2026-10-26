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
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The DocsGPT server renames tools ("write_file" → "write_file_ct0").
var toolSuffixRe = regexp.MustCompile(`_ct\d+$`)

// NormalizeName strips the suffix the server appends to tool names.
func NormalizeName(name string) string {
	return toolSuffixRe.ReplaceAllString(name, "")
}

var (
	errTimeout = errors.New("timed out")
	errMoved   = errors.New("moved to the background")
)

// runCommand runs command through the shell, copying its combined output
// to out. It gets at most timeout (errTimeout) and is killed when ctx is
// cancelled (context.Canceled). A non-zero exit is an *exec.ExitError.
func runCommand(ctx context.Context, command, dir string, timeout time.Duration, out io.Writer) error {
	_, err := runForeground(ctx, command, dir, timeout, out, nil)
	return err
}

// runForeground is runCommand, but a signal on move hands the command over
// still running: it returns it with errMoved, its output still going to
// out, which the caller then points elsewhere.
func runForeground(ctx context.Context, command, dir string, timeout time.Duration, out io.Writer, move <-chan struct{}) (*shellRun, error) {
	r, err := startShell(command, dir, &switchWriter{w: out}, nil)
	if err != nil {
		return nil, err
	}
	var expired <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expired = t.C
	}
	stop := func(why error) (*shellRun, error) {
		r.cancel()
		<-r.done
		if r.err == nil {
			return nil, nil
		}
		return nil, why
	}
	select {
	case <-r.done:
		switch {
		case r.err == nil:
			return nil, nil
		case ctx.Err() != nil:
			return nil, context.Canceled
		}
		return nil, r.err
	case <-ctx.Done():
		return stop(context.Canceled)
	case <-expired:
		return stop(errTimeout)
	case <-move:
		return r, errMoved
	}
}

// shellRun is a command started through the shell: done closes once it
// ended (err then set), cancel kills it with everything it started.
type shellRun struct {
	cmd    *exec.Cmd
	out    *switchWriter // stdout and stderr, when it was given one
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// startShell starts command through the shell, in a process group of its
// own (killProcessGroup), with its output on out and its stderr on errOut
// (out when nil). An *os.File gets the output directly; another writer
// through a pipe the command's children may hold open, which Wait gives up
// on a second after the command ends.
func startShell(command, dir string, out, errOut io.Writer) (*shellRun, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := shellCommand(ctx, command)
	killProcessGroup(cmd)
	// Don't wait forever for output from a process that escaped the kill.
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	if errOut == nil {
		errOut = out
	}
	cmd.Stdout, cmd.Stderr = out, errOut
	r := &shellRun{cmd: cmd, cancel: cancel, done: make(chan struct{})}
	r.out, _ = out.(*switchWriter)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	go func() {
		r.err = cmd.Wait()
		cancel()
		close(r.done)
	}()
	return r, nil
}

// switchWriter passes writes on to a writer that can be swapped while a
// command writes to it: a command moved to the background goes on into
// its log.
type switchWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *switchWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// handOver writes what before returns to w, then makes w the writer, with
// no write of the command's in between.
func (s *switchWriter) handOver(w io.Writer, before func() string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	io.WriteString(w, before())
	s.w = w
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
