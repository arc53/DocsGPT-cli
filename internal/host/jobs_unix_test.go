//go:build !windows

package host

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// childPID reads the pid a test command wrote, waiting for it to appear.
func childPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	waitFor(t, func() bool {
		b, err := os.ReadFile(path)
		if err != nil || !strings.HasSuffix(string(b), "\n") {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil
	})
	return pid
}

// assertDead fails when pid is still alive shortly after the command ended.
func assertDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("process %d survived the command's end", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestJobReportsOutputAndExitCode(t *testing.T) {
	srv := newDeviceServer(t)
	j := newTestJobs(t, srv, t.TempDir())
	j.Start(runInvocation("inv_ok", "echo out; echo err >&2; exit 3", 10_000))

	ctl := waitControl(t, srv)
	if ctl.ExitCode == nil || *ctl.ExitCode != 3 || ctl.Error != "" {
		t.Fatalf("control = %+v, want exit 3 and no error", ctl)
	}
	acks, chunks, _ := srv.received()
	if len(acks) != 1 || acks[0] != "accepted" {
		t.Errorf("acks = %v", acks)
	}
	var out, errOut string
	for _, c := range chunks {
		switch c.Stream {
		case "stdout":
			out += c.Chunk
		case "stderr":
			errOut += c.Chunk
		}
	}
	if out != "out\n" || errOut != "err\n" {
		t.Errorf("stdout %q stderr %q", out, errOut)
	}
	waitFor(t, j.Idle)
}

func TestJobDenylistNeverRuns(t *testing.T) {
	srv := newDeviceServer(t)
	j := newTestJobs(t, srv, t.TempDir())
	j.Start(runInvocation("inv_denied", "rm -rf / --no-preserve-root", 10_000))

	ctl := waitControl(t, srv)
	if ctl.Error != "command_blocked_by_denylist" {
		t.Errorf("control = %+v", ctl)
	}
	if acks, _, _ := srv.received(); len(acks) != 1 || acks[0] != "denied" {
		t.Errorf("acks = %v, want denied", acks)
	}
}

// TestJobCancelStopsProcessGroup: a cancel stops the shell and the children
// it started, and reports the command as cancelled.
func TestJobCancelStopsProcessGroup(t *testing.T) {
	srv := newDeviceServer(t)
	j := newTestJobs(t, srv, t.TempDir())
	pidFile := filepath.Join(t.TempDir(), "pid")
	j.Start(runInvocation("inv_cancel", "sleep 30 & echo $! > "+pidFile+"; wait", 60_000))
	child := childPID(t, pidFile)

	start := time.Now()
	j.Cancel("inv_cancel")
	ctl := waitControl(t, srv)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("cancel took %v", took)
	}
	if ctl.Error != "cancelled" || ctl.ExitCode == nil || *ctl.ExitCode != -1 {
		t.Errorf("control = %+v, want cancelled with exit -1", ctl)
	}
	assertDead(t, child)
	waitFor(t, j.Idle)
}

// TestJobCancelEscalatesToKill: a command that ignores SIGTERM is killed
// once the grace period ends.
func TestJobCancelEscalatesToKill(t *testing.T) {
	srv := newDeviceServer(t)
	j := newTestJobs(t, srv, t.TempDir())
	pidFile := filepath.Join(t.TempDir(), "pid")
	j.Start(runInvocation("inv_stubborn", "trap '' TERM; sleep 30 & echo $! > "+pidFile+"; wait", 60_000))
	child := childPID(t, pidFile)

	start := time.Now()
	j.Cancel("inv_stubborn")
	ctl := waitControl(t, srv)
	if took := time.Since(start); took < j.grace {
		t.Errorf("killed after %v, before the %v grace period", took, j.grace)
	}
	if ctl.Error != "cancelled" {
		t.Errorf("control = %+v", ctl)
	}
	assertDead(t, child)
}

// TestJobTimeoutStopsProcessGroup: a timeout stops the children too, not
// just the shell.
func TestJobTimeoutStopsProcessGroup(t *testing.T) {
	srv := newDeviceServer(t)
	j := newTestJobs(t, srv, t.TempDir())
	pidFile := filepath.Join(t.TempDir(), "pid")
	j.Start(runInvocation("inv_timeout", "sleep 30 & echo $! > "+pidFile+"; wait", 500))
	child := childPID(t, pidFile)

	ctl := waitControl(t, srv)
	if ctl.Error != "timeout" {
		t.Errorf("control = %+v, want timeout", ctl)
	}
	assertDead(t, child)
}

// TestJobCancelAfterFinishIsIgnored: the report of a finished command is
// not changed by a late cancel.
func TestJobCancelAfterFinishIsIgnored(t *testing.T) {
	srv := newDeviceServer(t)
	j := newTestJobs(t, srv, t.TempDir())
	j.Start(runInvocation("inv_done", "exit 0", 10_000))
	ctl := waitControl(t, srv)
	j.Cancel("inv_done")
	time.Sleep(50 * time.Millisecond)
	if ctl.Error != "" {
		t.Errorf("control = %+v", ctl)
	}
	if _, chunks, _ := srv.received(); len(chunks) != 1 {
		t.Errorf("chunks = %+v, want only the control", chunks)
	}
}

// TestJobsStopOnHostExit: when the daemon stops, a running command is
// stopped and reported as host_shutdown, and a command that arrives after
// that is reported without being run. (The test's name stays clear of the
// denylist: its temporary paths are part of the commands.)
func TestJobsStopOnHostExit(t *testing.T) {
	srv := newDeviceServer(t)
	dir := t.TempDir()
	j := newTestJobs(t, srv, dir)
	pidFile := filepath.Join(t.TempDir(), "pid")
	j.Start(runInvocation("inv_running", "sleep 30 & echo $! > "+pidFile+"; wait", 60_000))
	child := childPID(t, pidFile)

	j.Shutdown(context.Background())
	ctl := srv.control()
	if ctl == nil || ctl.Error != "host_shutdown" || ctl.ExitCode == nil || *ctl.ExitCode != -1 {
		t.Fatalf("control = %+v, want host_shutdown delivered before Shutdown returns", ctl)
	}
	assertDead(t, child)

	marker := filepath.Join(t.TempDir(), "ran")
	j.Start(runInvocation("inv_late", "touch "+marker, 10_000))
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a command that arrived during shutdown was run")
	}
	files := spoolFiles(t, dir)
	if len(files) != 1 || !strings.Contains(files[0], "inv_late") {
		t.Fatalf("spool = %v, want the late command's report kept for the next start", files)
	}
	again, err := loadOutbox(files[0], nil, fastPolicy(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.pending) != 1 || again.pending[0].Error != "host_shutdown" || !again.ackSent {
		t.Errorf("late report = %+v (ack pending %v)", again.pending, !again.ackSent)
	}
}

// TestJobFinishedOfflineIsReportedOnReconnect: the network is down when the
// command ends; the exit code arrives once the daemon is back online.
func TestJobFinishedOfflineIsReportedOnReconnect(t *testing.T) {
	srv := newDeviceServer(t)
	srv.setDown(true)
	j := newTestJobs(t, srv, t.TempDir())
	j.policy.First, j.policy.Max = time.Hour, time.Hour // only the kick retries
	j.Start(runInvocation("inv_offline", "echo done; exit 4", 10_000))

	waitFor(t, func() bool { return j.running() == 0 })
	if j.Idle() {
		t.Fatal("Idle with an undelivered report")
	}
	srv.setDown(false)
	j.Kick()
	ctl := waitControl(t, srv)
	if ctl.ExitCode == nil || *ctl.ExitCode != 4 {
		t.Errorf("control = %+v, want exit 4", ctl)
	}
	waitFor(t, j.Idle)
}

// TestJobReportSurvivesRestart: a daemon stops before it could report; the
// next one sends the report from the spool.
func TestJobReportSurvivesRestart(t *testing.T) {
	srv := newDeviceServer(t)
	srv.setDown(true)
	dir := t.TempDir()
	first := newTestJobs(t, srv, dir)
	first.flush = 50 * time.Millisecond
	first.Start(runInvocation("inv_restart", "echo before; exit 5", 10_000))
	waitFor(t, func() bool { return first.running() == 0 })
	first.Shutdown(context.Background())
	if files := spoolFiles(t, dir); len(files) != 1 {
		t.Fatalf("spool = %v after shutdown, want the unreported result", files)
	}

	srv.setDown(false)
	second := newTestJobs(t, srv, dir)
	if n := second.Recover(); n != 1 {
		t.Fatalf("Recover() = %d, want 1", n)
	}
	ctl := waitControl(t, srv)
	if ctl.ExitCode == nil || *ctl.ExitCode != 5 || ctl.Error != "" {
		t.Errorf("control = %+v, want exit 5", ctl)
	}
	waitFor(t, func() bool { return len(spoolFiles(t, dir)) == 0 })
}
