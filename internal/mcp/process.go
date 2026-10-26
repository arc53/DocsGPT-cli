package mcp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
)

// process is a server's command, talking JSON-RPC over its stdin and stdout.
type process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{} // closed once it exited
}

// startProcess runs s in its own process group (no controlling terminal; on
// Windows, a hidden console), its stderr going to log. It returns the
// process and its stdout, of which only JSON lines are kept: anything else
// (a banner, a PHP notice) would break the protocol, and goes to log.
func startProcess(s Server, log io.Writer) (*process, io.ReadCloser, error) {
	cmd := exec.Command(s.Command, s.Args...)
	cmd.Dir = s.Dir
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	ownGroup(cmd)
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, log
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, nil, err
	}
	p := &process{cmd: cmd, stdin: inW, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(p.done)
	}()

	r, w := io.Pipe()
	go func() {
		defer outR.Close()
		br := bufio.NewReaderSize(outR, 64<<10)
		for {
			line, err := br.ReadBytes('\n')
			if t := bytes.TrimSpace(line); len(t) > 0 {
				if t[0] == '{' || t[0] == '[' {
					if _, werr := w.Write(append(t, '\n')); werr != nil {
						return // the session is closed
					}
				} else {
					fmt.Fprintf(log, "stdout: %s\n", t)
				}
			}
			if err != nil {
				w.Close()
				return
			}
		}
	}()
	return p, r, nil
}

// exited reports whether the process has ended.
func (p *process) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// stop ends the process: its input closed, which ends a well-behaved
// server, then terminated and killed, its whole group with it (npx and uvx
// leave children behind).
func (p *process) stop() {
	p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(time.Second):
		terminate(p.cmd)
		select {
		case <-p.done:
		case <-time.After(time.Second):
			kill(p.cmd)
			<-p.done
		}
	}
	killOrphans(p.cmd)
}

// stderrLog keeps what a server writes to stderr (and to stdout outside
// the protocol): in ~/.docsgpt/logs/mcp-<name>.log, and its last lines in
// memory for errors and /mcp.
type stderrLog struct {
	path string
	mu   sync.Mutex
	file *os.File
	tail []string
	part string // an unfinished line
}

// maxLog is the size past which a server's log starts over.
const maxLog = 1 << 20

const tailLines = 20

func openLog(name string) *stderrLog {
	l := &stderrLog{path: filepath.Join(config.Dir(), "logs", "mcp-"+clean(name)+".log")}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return l
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(l.path); err == nil && fi.Size() > maxLog {
		flags |= os.O_TRUNC
	}
	l.file, _ = os.OpenFile(l.path, flags, 0o600)
	return l
}

func (l *stderrLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Write(p)
	}
	lines := strings.Split(l.part+string(p), "\n")
	l.part = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		if line = strings.TrimSpace(line); line != "" {
			l.tail = append(l.tail, line)
		}
	}
	if len(l.tail) > tailLines {
		l.tail = l.tail[len(l.tail)-tailLines:]
	}
	return len(p), nil
}

// mark writes a line of the CLI's own into the log (a start, a stop).
func (l *stderrLog) mark(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		fmt.Fprintf(l.file, "--- %s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	}
}

// last returns the last n lines written.
func (l *stderrLog) last(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	lines := l.tail
	if l.part != "" {
		lines = append(lines[:len(lines):len(lines)], strings.TrimSpace(l.part))
	}
	return lines[max(0, len(lines)-n):]
}

func (l *stderrLog) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}
