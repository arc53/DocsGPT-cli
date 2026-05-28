package host

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"time"

	"docsgpt-cli/internal/tools"
)

// ExecuteAndStream runs the invocation locally and streams stdout/stderr to
// the server via chunked POST.
func ExecuteAndStream(ctx context.Context, t *Transport, sessionID string, inv Invocation) {
	// CLI-side safety floor.
	command, _ := inv.Params["command"].(string)
	workingDir, _ := inv.Params["working_directory"].(string)
	timeoutMs := 30000
	if v, ok := inv.Params["timeout_ms"].(float64); ok {
		timeoutMs = int(v)
	}
	if safe, reason := tools.IsSafe(command); !safe {
		_ = t.PostAck(ctx, sessionID, inv.InvocationID, "denied", "denied_by_safety")
		_ = postControl(ctx, t, sessionID, inv.InvocationID, 0, "command_blocked_by_denylist", reason)
		return
	}
	// Effective approval mode is already resolved server-side. The CLI
	// honors it without re-prompting (no human at the device).
	_ = t.PostAck(ctx, sessionID, inv.InvocationID, "accepted", "writes_only_passthrough")

	timeout := time.Duration(timeoutMs) * time.Millisecond
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", command)
	}
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	stdout := &lineStreamer{
		t:            t,
		sessionID:    sessionID,
		invocationID: inv.InvocationID,
		stream:       "stdout",
		seq:          0,
	}
	stderr := &lineStreamer{
		t:            t,
		sessionID:    sessionID,
		invocationID: inv.InvocationID,
		stream:       "stderr",
		seq:          0,
		parent:       stdout,
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	err := cmd.Run()
	stdout.flush()
	stderr.flush()
	duration := time.Since(start).Milliseconds()

	exitCode := 0
	errMsg := ""
	if cmdCtx.Err() == context.DeadlineExceeded {
		errMsg = "timeout"
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if errMsg == "" {
			errMsg = err.Error()
		}
	}
	_ = postControl(ctx, t, sessionID, inv.InvocationID, exitCode, "", "")
	_ = postControlFull(ctx, t, sessionID, inv.InvocationID, exitCode, int(duration), errMsg, stderr.nextSeq())
}

type lineStreamer struct {
	t            *Transport
	sessionID    string
	invocationID string
	stream       string
	seq          int
	parent       *lineStreamer
	buf          bytes.Buffer
}

func (l *lineStreamer) Write(p []byte) (int, error) {
	l.buf.Write(p)
	for {
		line, rest, found := splitOnce(l.buf.Bytes(), '\n')
		if !found {
			break
		}
		l.send(string(line) + "\n")
		l.buf.Reset()
		l.buf.Write(rest)
	}
	if l.buf.Len() > 16*1024 {
		l.send(l.buf.String())
		l.buf.Reset()
	}
	return len(p), nil
}

func (l *lineStreamer) flush() {
	if l.buf.Len() > 0 {
		l.send(l.buf.String())
		l.buf.Reset()
	}
}

func (l *lineStreamer) send(chunk string) {
	if chunk == "" {
		return
	}
	payload := map[string]interface{}{
		"stream": l.stream,
		"chunk":  chunk,
		"seq":    l.nextSeq(),
	}
	body, _ := json.Marshal(payload)
	_ = l.t.PostOutput(context.Background(), l.sessionID, l.invocationID, append(body, '\n'))
}

func (l *lineStreamer) nextSeq() int {
	if l.parent != nil {
		return l.parent.nextSeq()
	}
	s := l.seq
	l.seq++
	return s
}

func splitOnce(buf []byte, sep byte) ([]byte, []byte, bool) {
	idx := bytes.IndexByte(buf, sep)
	if idx < 0 {
		return nil, buf, false
	}
	return buf[:idx], buf[idx+1:], true
}

func postControl(ctx context.Context, t *Transport, sessionID, invocationID string, exitCode int, errCode, detail string) error {
	if errCode == "" {
		return nil
	}
	payload := map[string]interface{}{
		"stream":    "control",
		"exit_code": exitCode,
		"error":     errCode,
		"detail":    detail,
		"seq":       0,
	}
	body, _ := json.Marshal(payload)
	return t.PostOutput(ctx, sessionID, invocationID, append(body, '\n'))
}

func postControlFull(ctx context.Context, t *Transport, sessionID, invocationID string, exitCode, durationMs int, errMsg string, seq int) error {
	payload := map[string]interface{}{
		"stream":      "control",
		"exit_code":   exitCode,
		"duration_ms": durationMs,
		"seq":         seq,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	body, _ := json.Marshal(payload)
	return t.PostOutput(ctx, sessionID, invocationID, append(body, '\n'))
}

