package host

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestJobs(t *testing.T, srv *deviceServer, dir string) *Jobs {
	t.Helper()
	tr := newTestTransport(srv.URL)
	tr.Baton.SetSessionID("st_test")
	j := newJobs(tr, "dev_test", dir)
	j.policy = fastPolicy()
	j.grace = 300 * time.Millisecond
	j.flush = 2 * time.Second
	j.logf = quiet
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // force: kill anything a failed test left running
		j.Shutdown(ctx)
	})
	return j
}

func runInvocation(id, command string, timeoutMs float64) Invocation {
	params := map[string]interface{}{"command": command}
	if timeoutMs > 0 {
		params["timeout_ms"] = timeoutMs
	}
	return Invocation{InvocationID: id, Action: "run_command", Params: params}
}

// waitControl waits for the server to receive the control chunk.
func waitControl(t *testing.T, srv *deviceServer) chunk {
	t.Helper()
	var ctl *chunk
	waitFor(t, func() bool { ctl = srv.control(); return ctl != nil })
	return *ctl
}

func TestCommandTimeout(t *testing.T) {
	cases := []struct {
		params map[string]interface{}
		want   time.Duration
	}{
		{map[string]interface{}{}, 30 * time.Second},
		{map[string]interface{}{"timeout_ms": float64(3_600_000)}, time.Hour},
		{map[string]interface{}{"timeout_ms": float64(7_200_000)}, 2 * time.Hour},
		{map[string]interface{}{"timeout_ms": float64(0)}, 30 * time.Second},
		{map[string]interface{}{"timeout_ms": "600000"}, 30 * time.Second},
		{map[string]interface{}{"timeout_ms": math.MaxFloat64}, time.Duration(math.MaxInt64)},
	}
	for _, c := range cases {
		if got := commandTimeout(c.params); got != c.want {
			t.Errorf("commandTimeout(%v) = %v, want %v", c.params, got, c.want)
		}
	}
}

func TestCancelUnknownInvocationIsIgnored(t *testing.T) {
	srv := newDeviceServer(t)
	j := newTestJobs(t, srv, t.TempDir())
	j.Cancel("inv_unknown")
	if _, _, posts := srv.received(); posts != 0 {
		t.Errorf("a cancel for an unknown invocation posted %d requests", posts)
	}
}

// TestRecoverReportsInterruptedCommand: a spool left by a daemon that died
// while its command ran is reported as interrupted, after the output it holds.
func TestRecoverReportsInterruptedCommand(t *testing.T) {
	srv := newDeviceServer(t)
	dir := t.TempDir()
	left := newOutbox(dir, testMeta("inv_crashed"), nil, fastPolicy(), quiet)
	left.setPID(31337)
	left.output("stdout", "halfway\n")
	left.finish(false)

	j := newTestJobs(t, srv, dir)
	if n := j.Recover(); n != 1 {
		t.Fatalf("Recover() = %d, want 1", n)
	}
	ctl := waitControl(t, srv)
	if ctl.Error != "interrupted" || ctl.ExitCode == nil || *ctl.ExitCode != -1 {
		t.Errorf("control = %+v, want interrupted with exit -1", ctl)
	}
	if !strings.Contains(ctl.Detail, "31337") {
		t.Errorf("detail %q does not name the process", ctl.Detail)
	}
	_, chunks, _ := srv.received()
	if chunks[0].Chunk != "halfway\n" || chunks[1].Seq != 1 {
		t.Errorf("chunks = %+v", chunks)
	}
	waitFor(t, func() bool { return len(spoolFiles(t, dir)) == 0 })
}

// TestRecoverSkipsOtherDevicesAndExpiredReports: a spool from a previous
// pairing, or past its give-up time, is deleted without being sent.
func TestRecoverSkipsOtherDevicesAndExpiredReports(t *testing.T) {
	srv := newDeviceServer(t)
	dir := t.TempDir()

	other := testMeta("inv_other_device")
	other.DeviceID = "dev_previous"
	newOutbox(dir, other, nil, fastPolicy(), quiet).finish(false)

	expired := testMeta("inv_expired")
	expired.GiveUpAt = time.Now().Add(-time.Minute).Unix()
	o := newOutbox(dir, expired, nil, fastPolicy(), quiet)
	o.end(chunk{ExitCode: intPtr(0)})
	o.finish(false)

	j := newTestJobs(t, srv, dir)
	if n := j.Recover(); n != 0 {
		t.Errorf("Recover() = %d, want 0", n)
	}
	if files := spoolFiles(t, dir); len(files) != 0 {
		t.Errorf("spool files left: %v", files)
	}
	if _, _, posts := srv.received(); posts != 0 {
		t.Errorf("posted %d requests for reports that should be dropped", posts)
	}
}

// TestSpoolIsLockedToOneDaemon: a second daemon on the same machine does
// not share (and replay) the first one's spool.
func TestSpoolIsLockedToOneDaemon(t *testing.T) {
	srv := newDeviceServer(t)
	dir := filepath.Join(t.TempDir(), "host-spool")
	first := newTestJobs(t, srv, "")
	if err := first.openSpool(dir); err != nil {
		t.Fatal(err)
	}
	second := newTestJobs(t, srv, "")
	if err := second.openSpool(dir); err == nil {
		t.Fatal("a second daemon locked the same spool")
	}
	if second.dir != "" {
		t.Errorf("second daemon uses the spool %q, want memory only", second.dir)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0700 {
		t.Errorf("spool dir: %v, mode %v", err, info.Mode().Perm())
	}
}

func TestClearLocalStateRemovesSpool(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	if err := os.MkdirAll(SpoolDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(SpoolDir(), "inv_x"+spoolExt), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !HasLocalState() {
		t.Fatal("HasLocalState() = false with a spool")
	}
	removed, err := ClearLocalState()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "host-spool" {
		t.Errorf("removed = %v", removed)
	}
	if _, err := os.Stat(SpoolDir()); !os.IsNotExist(err) {
		t.Errorf("spool still there: %v", err)
	}
}
