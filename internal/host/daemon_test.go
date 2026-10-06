package host

import (
	"testing"
)

// TestHoldForRestartWaitsForRunningCommands: an update never restarts the
// daemon while a command runs or a report is undelivered, and while it
// restarts no session can open.
func TestHoldForRestartWaitsForRunningCommands(t *testing.T) {
	srv := newDeviceServer(t)
	j := newTestJobs(t, srv, t.TempDir())
	tr := j.t

	j.mu.Lock()
	j.runs["inv_busy"] = &run{stop: make(chan struct{}), kill: make(chan struct{})}
	j.mu.Unlock()
	if holdForRestart(tr, j) {
		t.Fatal("restart allowed while a command runs")
	}
	if tr.Baton.State() != StatePolling {
		t.Fatalf("baton = %s, want polling after a refused restart", tr.Baton.State())
	}

	j.mu.Lock()
	delete(j.runs, "inv_busy")
	j.mu.Unlock()
	if !holdForRestart(tr, j) {
		t.Fatal("restart refused while idle")
	}
	if tr.Baton.Transition(StatePolling, StateStreaming) {
		t.Error("a session could open while the daemon restarts")
	}
}
