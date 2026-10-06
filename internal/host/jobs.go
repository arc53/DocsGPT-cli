package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/display"
)

// Jobs runs the daemon's invocations and delivers their reports. A command
// runs in its own goroutine under no session, so it outlives the SSE session
// that delivered it; its ack, output and exit code go through an outbox.
type Jobs struct {
	t        *Transport
	deviceID string
	dir      string // the spool; "" delivers from memory only
	policy   deliveryPolicy
	grace    time.Duration // SIGTERM to SIGKILL
	flush    time.Duration // how long shutdown waits for reports to go out
	logf     func(string)

	sendCtx  context.Context // ends the deliveries once shutdown is done
	stopSend context.CancelFunc

	mu         sync.Mutex
	closed     bool
	runs       map[string]*run
	delivering int           // outboxes still delivering
	changed    chan struct{} // closed (and replaced) when runs or delivering shrink
	kick       chan struct{} // closed (and replaced) by Kick
	lockFile   *os.File      // holds the spool lock for the daemon's lifetime
}

// run is one command being run.
type run struct {
	once   sync.Once
	reason string        // set before stop closes
	stop   chan struct{} // closed to stop the command (cancel, shutdown)
	kill   chan struct{} // closed to skip the grace period
	force  sync.Once
}

func (r *run) halt(reason string) {
	r.once.Do(func() {
		r.reason = reason
		close(r.stop)
	})
}

func newJobs(t *Transport, deviceID, dir string) *Jobs {
	ctx, cancel := context.WithCancel(context.Background())
	return &Jobs{
		t:        t,
		deviceID: deviceID,
		dir:      dir,
		policy:   defaultPolicy(),
		grace:    5 * time.Second,
		flush:    5 * time.Second,
		logf:     func(s string) { fmt.Println(display.Muted(LogStamp(time.Now()) + " " + s)) },
		sendCtx:  ctx,
		stopSend: cancel,
		runs:     map[string]*run{},
		changed:  make(chan struct{}),
		kick:     make(chan struct{}),
	}
}

// openSpool creates the spool directory and locks it for this daemon. On an
// error the daemon delivers from memory only.
func (j *Jobs) openSpool(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return fmt.Errorf("another docsgpt-cli host is using %s", dir)
	}
	j.dir, j.lockFile = dir, f
	return nil
}

// Start runs an invocation in the background. Called from the SSE loop, so
// a cancel that arrives later on the same stream always finds it.
func (j *Jobs) Start(inv Invocation) {
	id := inv.InvocationID
	if id == "" {
		return
	}
	j.mu.Lock()
	if j.closed {
		j.mu.Unlock()
		// The server already handed it over, so say why it never ran.
		ob := j.open(j.meta(inv, 0))
		ob.end(chunk{ExitCode: intPtr(-1), Error: "host_shutdown", Detail: "the host was shutting down; the command was not run"})
		return
	}
	if _, dup := j.runs[id]; dup {
		j.mu.Unlock()
		return
	}
	r := &run{stop: make(chan struct{}), kill: make(chan struct{})}
	j.runs[id] = r
	j.mu.Unlock()
	go j.execute(inv, r)
}

// Cancel stops a running invocation. One that is unknown or already
// finished is ignored: its report stands.
func (j *Jobs) Cancel(id string) {
	j.mu.Lock()
	r := j.runs[id]
	j.mu.Unlock()
	if r == nil {
		j.logf(id + ": cancel ignored, the command is not running")
		return
	}
	j.logf(id + ": cancelled, stopping the command")
	r.halt("cancelled")
}

// Kick makes every outbox waiting to retry try again now (the daemon is
// back online).
func (j *Jobs) Kick() {
	j.mu.Lock()
	close(j.kick)
	j.kick = make(chan struct{})
	j.mu.Unlock()
}

func (j *Jobs) kicked() <-chan struct{} {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.kick
}

// Idle reports whether no command runs and every report was delivered, so
// the daemon may restart.
func (j *Jobs) Idle() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.runs) == 0 && j.delivering == 0
}

// running is how many commands are running.
func (j *Jobs) running() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.runs)
}

// meta describes an invocation's report. It is given up an hour after the
// command's own timeout would have ended it.
func (j *Jobs) meta(inv Invocation, timeout time.Duration) spoolMeta {
	return spoolMeta{
		InvocationID: inv.InvocationID,
		SessionID:    j.t.Baton.SessionID(),
		DeviceID:     j.deviceID,
		GiveUpAt:     time.Now().Add(timeout).Add(time.Hour).Unix(),
	}
}

func (j *Jobs) execute(inv Invocation, r *run) {
	defer func() {
		j.mu.Lock()
		delete(j.runs, inv.InvocationID)
		j.notify()
		j.mu.Unlock()
	}()
	command, _ := inv.Params["command"].(string)
	workingDir, _ := inv.Params["working_directory"].(string)
	timeout := commandTimeout(inv.Params)
	meta := j.meta(inv, timeout)

	if reason := blocked(command); reason != "" {
		meta.Decision, meta.Reason = "denied", "denied_by_safety"
		j.open(meta).end(chunk{ExitCode: intPtr(0), Error: "command_blocked_by_denylist", Detail: reason})
		return
	}
	// The approval mode is resolved server-side; nobody is at the device to
	// ask, so the daemon follows it.
	meta.Decision, meta.Reason = "accepted", "writes_only_passthrough"
	ob := j.open(meta)
	select {
	case <-r.stop:
		ob.end(chunk{ExitCode: intPtr(-1), Error: r.reason})
		return
	default:
	}

	stdout := &lineStreamer{ob: ob, stream: "stdout"}
	stderr := &lineStreamer{ob: ob, stream: "stderr"}
	res := runCommand(command, workingDir, timeout, j.grace, r.stop, r.kill, stdout, stderr, ob.setPID)
	stdout.flush()
	stderr.flush()

	c := chunk{ExitCode: intPtr(res.exitCode), DurationMs: res.duration.Milliseconds()}
	switch {
	case res.stopped:
		c.Error = r.reason
	case res.timedOut:
		c.Error = "timeout"
	case res.err != nil:
		c.Error = res.err.Error()
	}
	ob.end(c)
}

// open starts delivering an invocation's report.
func (j *Jobs) open(meta spoolMeta) *outbox {
	ob := newOutbox(j.dir, meta, j.t.post, j.policy, j.logf)
	j.track(ob)
	return ob
}

func (j *Jobs) track(ob *outbox) {
	j.mu.Lock()
	j.delivering++
	j.mu.Unlock()
	go func() {
		ob.deliver(j.sendCtx, j.kicked)
		j.mu.Lock()
		j.delivering--
		j.notify()
		j.mu.Unlock()
	}()
}

// notify wakes waitUntil. Caller holds mu.
func (j *Jobs) notify() {
	close(j.changed)
	j.changed = make(chan struct{})
}

// waitUntil waits for cond (checked under mu) and returns false if stop
// closes or limit passes first.
func (j *Jobs) waitUntil(cond func() bool, stop <-chan struct{}, limit <-chan time.Time) bool {
	for {
		j.mu.Lock()
		ok, changed := cond(), j.changed
		j.mu.Unlock()
		if ok {
			return true
		}
		select {
		case <-changed:
		case <-stop:
			return false
		case <-limit:
			return false
		}
	}
}

// Recover delivers the reports a previous daemon left in the spool and
// returns how many. A command that was still running then cannot be
// reattached: it is reported as interrupted. Reports for another pairing,
// or past their give-up time, are deleted.
func (j *Jobs) Recover() int {
	if j.dir == "" {
		return 0
	}
	tmps, _ := filepath.Glob(filepath.Join(j.dir, "*"+spoolExt+".tmp"))
	for _, p := range tmps {
		os.Remove(p)
	}
	paths, _ := filepath.Glob(filepath.Join(j.dir, "*"+spoolExt))
	n := 0
	for _, p := range paths {
		ob, err := loadOutbox(p, j.t.post, j.policy, j.logf)
		if err != nil {
			j.logf(fmt.Sprintf("dropped an unreadable spool file %s: %v", filepath.Base(p), err))
			os.Remove(p)
			continue
		}
		if ob.meta.DeviceID != j.deviceID || time.Now().Unix() >= ob.meta.GiveUpAt {
			ob.finish(true)
			continue
		}
		if !ob.ended {
			detail := "the host stopped while the command was running"
			if ob.pid > 0 {
				detail += fmt.Sprintf("; process %d may still be running", ob.pid)
			}
			ob.end(chunk{ExitCode: intPtr(-1), Error: "interrupted", Detail: detail})
		}
		j.track(ob)
		n++
	}
	return n
}

// Shutdown stops accepting commands, stops the running ones (reported as
// host_shutdown) and waits up to the flush time for reports to go out;
// what is left stays in the spool for the next start. When force closes,
// commands are killed at once and nothing more is waited for.
func (j *Jobs) Shutdown(force context.Context) {
	j.mu.Lock()
	j.closed = true
	stopping := make([]*run, 0, len(j.runs))
	for _, r := range j.runs {
		r.halt("host_shutdown")
		stopping = append(stopping, r)
	}
	j.mu.Unlock()

	idle := func() bool { return len(j.runs) == 0 }
	if !j.waitUntil(idle, force.Done(), nil) {
		for _, r := range stopping {
			r.force.Do(func() { close(r.kill) })
		}
		j.waitUntil(idle, nil, time.After(5*time.Second))
	}
	j.Kick()
	j.waitUntil(func() bool { return j.delivering == 0 }, force.Done(), time.After(j.flush))
	j.stopSend()
	j.waitUntil(func() bool { return j.delivering == 0 }, nil, time.After(2*time.Second))
}
