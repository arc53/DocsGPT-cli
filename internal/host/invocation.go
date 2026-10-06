package host

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// denylist is the host's safety floor: invocations run without a person at
// the device to approve them.
var denylist = []string{
	"rm -rf /",
	"rm -rf /*",
	"mkfs",
	"dd if=",
	"> /dev/sd",
	"> /dev/nvme",
	"shutdown",
	"reboot",
	":(){ :|:& };:",
}

// blocked returns the denylist pattern command contains, or "".
func blocked(command string) string {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, p := range denylist {
		if strings.Contains(lower, p) {
			return fmt.Sprintf("blocked pattern: %q", p)
		}
	}
	return ""
}

// defaultTimeout applies when the server sends no timeout_ms.
const defaultTimeout = 30 * time.Second

// commandTimeout is the invocation's timeout_ms. Any positive value is
// honoured (a background command may run for an hour or more); only the
// conversion is bounded so a huge value cannot overflow.
func commandTimeout(params map[string]interface{}) time.Duration {
	ms, ok := params["timeout_ms"].(float64)
	if !ok || ms <= 0 {
		return defaultTimeout
	}
	if ms >= float64(math.MaxInt64/int64(time.Millisecond)) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ms) * time.Millisecond
}

// commandResult is how a command ended.
type commandResult struct {
	exitCode int
	stopped  bool  // stop closed before the command ended
	timedOut bool  // the timeout passed first
	err      error // the command could not be run
	duration time.Duration
}

// runCommand runs command in a process group of its own and copies its
// output to stdout and stderr. It returns once the command has exited and
// its output is drained. When the timeout passes or stop closes first, the
// group gets SIGTERM, then SIGKILL after grace (or at once when kill closes).
func runCommand(command, dir string, timeout, grace time.Duration, stop, kill <-chan struct{},
	stdout, stderr io.Writer, started func(pid int)) commandResult {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Dir = dir
	newGroup(cmd)

	// Pipes of our own rather than exec's: Wait then returns when the shell
	// exits, and this function decides how long to wait for the output.
	outR, outW, err := os.Pipe()
	if err != nil {
		return commandResult{err: err}
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return commandResult{err: err}
	}
	defer outR.Close()
	defer errR.Close()
	cmd.Stdout, cmd.Stderr = outW, errW
	begin := time.Now()
	err = cmd.Start()
	outW.Close()
	errW.Close()
	if err != nil {
		return commandResult{err: err}
	}
	started(cmd.Process.Pid)

	copied := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Go(func() { _, _ = io.Copy(stdout, outR) })
		wg.Go(func() { _, _ = io.Copy(stderr, errR) })
		wg.Wait()
		close(copied)
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	var waitErr error
	exitedCh, copiedCh := (<-chan error)(exited), (<-chan struct{})(copied)
	// finished waits for the exit and the end of the output; false when
	// limit or interrupt comes first.
	finished := func(limit <-chan time.Time, interrupt <-chan struct{}) bool {
		for exitedCh != nil || copiedCh != nil {
			select {
			case waitErr = <-exitedCh:
				exitedCh = nil
			case <-copiedCh:
				copiedCh = nil
			case <-limit:
				return false
			case <-interrupt:
				return false
			}
		}
		return true
	}

	var res commandResult
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for exitedCh != nil || copiedCh != nil {
		select {
		case waitErr = <-exitedCh:
			exitedCh = nil
			continue
		case <-copiedCh:
			copiedCh = nil
			continue
		case <-timer.C:
			res.timedOut = true
		case <-stop:
			res.stopped = true
		}
		stopGroup(cmd, false)
		finished(time.After(grace), kill)
		// Also reaches members of the group that closed their output.
		stopGroup(cmd, true)
		if !finished(time.After(2*time.Second), nil) {
			// Something outside the group holds the output open.
			outR.Close()
			errR.Close()
			finished(nil, nil)
		}
	}
	res.duration = time.Since(begin)
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			res.exitCode = exitErr.ExitCode()
		} else {
			res.err = waitErr
		}
	}
	return res
}

// lineStreamer queues a stream's output in an outbox a line at a time
// (or 16 KiB at a time for long lines). One goroutine writes to it.
type lineStreamer struct {
	ob     *outbox
	stream string
	buf    bytes.Buffer
}

func (l *lineStreamer) Write(p []byte) (int, error) {
	l.buf.Write(p)
	for {
		line, rest, found := splitOnce(l.buf.Bytes(), '\n')
		if !found {
			break
		}
		l.ob.output(l.stream, string(line)+"\n")
		l.buf.Reset()
		l.buf.Write(rest)
	}
	if l.buf.Len() > 16*1024 {
		l.flush()
	}
	return len(p), nil
}

func (l *lineStreamer) flush() {
	if l.buf.Len() > 0 {
		l.ob.output(l.stream, l.buf.String())
		l.buf.Reset()
	}
}

func splitOnce(buf []byte, sep byte) ([]byte, []byte, bool) {
	idx := bytes.IndexByte(buf, sep)
	if idx < 0 {
		return nil, buf, false
	}
	return buf[:idx], buf[idx+1:], true
}
