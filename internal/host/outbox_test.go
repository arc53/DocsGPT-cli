package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// deviceServer fakes the server's ack and output routes. It records what it
// accepted, in order, and can fail requests with a status or drop the
// connection (a network error).
type deviceServer struct {
	*httptest.Server

	mu        sync.Mutex
	down      bool // close the connection without answering
	status    int  // answer every request with this status (0: 200)
	failNext  int  // answer this many more requests with 503
	ackStatus int  // answer acks with this status (0: 200)
	posts     int
	acks      []string // decisions
	chunks    []chunk
	headers   []http.Header
}

func newDeviceServer(t *testing.T) *deviceServer {
	s := &deviceServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *deviceServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts++
	s.headers = append(s.headers, r.Header.Clone())
	if s.down {
		hj, ok := w.(http.Hijacker)
		if !ok {
			panic("no hijacker")
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			conn.Close()
		}
		return
	}
	if s.failNext > 0 {
		s.failNext--
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/ack") && s.ackStatus != 0:
		w.WriteHeader(s.ackStatus)
		return
	case strings.HasSuffix(r.URL.Path, "/ack"):
		var body struct{ Decision string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.acks = append(s.acks, body.Decision)
	case strings.HasSuffix(r.URL.Path, "/output"):
		sc := bufio.NewScanner(r.Body)
		sc.Buffer(nil, 4<<20)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var c chunk
			if err := json.Unmarshal(line, &c); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			s.chunks = append(s.chunks, c)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
}

func (s *deviceServer) setDown(down bool) {
	s.mu.Lock()
	s.down = down
	s.mu.Unlock()
}

func (s *deviceServer) setStatus(status int) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

func (s *deviceServer) received() ([]string, []chunk, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.acks...), append([]chunk(nil), s.chunks...), s.posts
}

// control returns the control chunk the server got, or nil.
func (s *deviceServer) control() *chunk {
	_, chunks, _ := s.received()
	for i := range chunks {
		if chunks[i].Stream == "control" {
			return &chunks[i]
		}
	}
	return nil
}

// fastPolicy retries quickly so tests do not wait for real backoff.
func fastPolicy() deliveryPolicy {
	p := defaultPolicy()
	p.First = 5 * time.Millisecond
	p.Max = 20 * time.Millisecond
	p.PostTimeout = 2 * time.Second
	return p
}

func testMeta(id string) spoolMeta {
	return spoolMeta{
		InvocationID: id,
		SessionID:    "st_test",
		DeviceID:     "dev_test",
		GiveUpAt:     time.Now().Add(time.Hour).Unix(),
		Decision:     "accepted",
		Reason:       "writes_only_passthrough",
	}
}

func noKick() <-chan struct{} { return nil }

func quiet(string) {}

// deliverAsync runs deliver in the background and returns its result channel.
func deliverAsync(ctx context.Context, o *outbox, kicked func() <-chan struct{}) <-chan bool {
	done := make(chan bool, 1)
	go func() { done <- o.deliver(ctx, kicked) }()
	return done
}

func waitDelivered(t *testing.T, done <-chan bool, want bool) {
	t.Helper()
	select {
	case got := <-done:
		if got != want {
			t.Fatalf("deliver returned %v, want %v", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deliver did not return")
	}
}

func spoolFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*"+spoolExt))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestOutboxDeliversAckThenChunksInOrder(t *testing.T) {
	srv := newDeviceServer(t)
	tr := newTestTransport(srv.URL)
	dir := t.TempDir()

	o := newOutbox(dir, testMeta("inv_order"), tr.post, fastPolicy(), quiet)
	if len(spoolFiles(t, dir)) != 1 {
		t.Fatal("expected a spool file while the invocation is unreported")
	}
	o.output("stdout", "a\n")
	o.output("stderr", "b\n")
	o.output("stdout", "c\n")
	o.end(chunk{ExitCode: intPtr(3), DurationMs: 12})

	waitDelivered(t, deliverAsync(context.Background(), o, noKick), true)

	acks, chunks, _ := srv.received()
	if len(acks) != 1 || acks[0] != "accepted" {
		t.Fatalf("acks = %v, want one accepted", acks)
	}
	want := []struct {
		stream, text string
	}{{"stdout", "a\n"}, {"stderr", "b\n"}, {"stdout", "c\n"}, {"control", ""}}
	if len(chunks) != len(want) {
		t.Fatalf("got %d chunks, want %d: %+v", len(chunks), len(want), chunks)
	}
	for i, w := range want {
		if chunks[i].Stream != w.stream || chunks[i].Chunk != w.text || chunks[i].Seq != i {
			t.Errorf("chunk %d = %+v, want %s %q seq %d", i, chunks[i], w.stream, w.text, i)
		}
	}
	ctl := chunks[3]
	if ctl.ExitCode == nil || *ctl.ExitCode != 3 || ctl.DurationMs != 12 || ctl.Truncated {
		t.Errorf("control = %+v", ctl)
	}
	if files := spoolFiles(t, dir); len(files) != 0 {
		t.Errorf("spool not removed after delivery: %v", files)
	}
}

// TestChunkWireFormat pins the JSON the server parses: exit_code is always
// present on a control chunk, even when it is 0.
func TestChunkWireFormat(t *testing.T) {
	b, _ := json.Marshal(chunk{Stream: "control", Seq: 4, ExitCode: intPtr(0), DurationMs: 7})
	if got, want := string(b), `{"stream":"control","seq":4,"exit_code":0,"duration_ms":7}`; got != want {
		t.Errorf("control = %s, want %s", got, want)
	}
	b, _ = json.Marshal(chunk{Stream: "stdout", Chunk: "hi\n", Seq: 0})
	if got, want := string(b), `{"stream":"stdout","chunk":"hi\n","seq":0}`; got != want {
		t.Errorf("stdout = %s, want %s", got, want)
	}
}

func TestOutboxRetriesServerErrors(t *testing.T) {
	srv := newDeviceServer(t)
	srv.failNext = 3
	tr := newTestTransport(srv.URL)

	o := newOutbox(t.TempDir(), testMeta("inv_retry"), tr.post, fastPolicy(), quiet)
	o.output("stdout", "x\n")
	o.end(chunk{ExitCode: intPtr(0)})
	waitDelivered(t, deliverAsync(context.Background(), o, noKick), true)

	acks, chunks, posts := srv.received()
	if len(acks) != 1 || len(chunks) != 2 {
		t.Fatalf("acks %v chunks %+v", acks, chunks)
	}
	if posts < 5 {
		t.Errorf("posts = %d, want the 3 failures retried", posts)
	}
}

func TestOutboxGivesUpWhenServerForgetsInvocation(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := newDeviceServer(t)
			srv.setStatus(status)
			tr := newTestTransport(srv.URL)
			dir := t.TempDir()

			o := newOutbox(dir, testMeta("inv_gone"), tr.post, fastPolicy(), quiet)
			o.output("stdout", "x\n")
			o.end(chunk{ExitCode: intPtr(0)})
			waitDelivered(t, deliverAsync(context.Background(), o, noKick), false)

			if _, _, posts := srv.received(); posts != 1 {
				t.Errorf("posts = %d, want 1 (no retry after %d)", posts, status)
			}
			if files := spoolFiles(t, dir); len(files) != 0 {
				t.Errorf("spool kept after %d: %v", status, files)
			}
		})
	}
}

// TestOutboxSkipsRejectedAck: an ack the server refuses as malformed is not
// retried forever, and the output after it is still delivered.
func TestOutboxSkipsRejectedAck(t *testing.T) {
	srv := newDeviceServer(t)
	srv.ackStatus = http.StatusBadRequest
	tr := newTestTransport(srv.URL)
	o := newOutbox(t.TempDir(), testMeta("inv_badack"), tr.post, fastPolicy(), quiet)
	o.end(chunk{ExitCode: intPtr(0)})
	waitDelivered(t, deliverAsync(context.Background(), o, noKick), true)
	if srv.control() == nil {
		t.Fatal("control chunk not delivered after a rejected ack")
	}
}

// TestOutboxDeliversAfterNetworkReturns: the command finishes while the
// network is down; the exit code arrives once it is back.
func TestOutboxDeliversAfterNetworkReturns(t *testing.T) {
	srv := newDeviceServer(t)
	srv.setDown(true)
	tr := newTestTransport(srv.URL)

	o := newOutbox(t.TempDir(), testMeta("inv_offline"), tr.post, fastPolicy(), quiet)
	done := deliverAsync(context.Background(), o, noKick)
	o.output("stdout", "partial\n")
	o.end(chunk{ExitCode: intPtr(7)})

	time.Sleep(100 * time.Millisecond) // several failed attempts
	if _, _, posts := srv.received(); posts < 2 {
		t.Fatalf("posts = %d while down, want retries", posts)
	}
	srv.setDown(false)
	waitDelivered(t, done, true)

	ctl := srv.control()
	if ctl == nil || ctl.ExitCode == nil || *ctl.ExitCode != 7 {
		t.Fatalf("control = %+v, want exit 7", ctl)
	}
}

// TestOutboxBoundsPendingOutput: while undeliverable, output past the budget
// drops the oldest stdout/stderr chunks; the control chunk is kept and says
// output was dropped.
func TestOutboxBoundsPendingOutput(t *testing.T) {
	srv := newDeviceServer(t)
	srv.setDown(true)
	tr := newTestTransport(srv.URL)
	p := fastPolicy()
	p.Budget = 100
	dir := t.TempDir()

	o := newOutbox(dir, testMeta("inv_bound"), tr.post, p, quiet)
	for i := 0; i < 50; i++ {
		o.output("stdout", "123456789\n") // 10 bytes each
	}
	o.end(chunk{ExitCode: intPtr(0)})

	o.mu.Lock()
	pendingBytes := o.outBytes
	o.mu.Unlock()
	if pendingBytes > p.Budget {
		t.Fatalf("pending output = %d bytes, budget %d", pendingBytes, p.Budget)
	}

	srv.setDown(false)
	waitDelivered(t, deliverAsync(context.Background(), o, noKick), true)
	_, chunks, _ := srv.received()
	if len(chunks) != 11 {
		t.Fatalf("got %d chunks, want the newest 10 and the control", len(chunks))
	}
	if chunks[0].Seq != 40 {
		t.Errorf("first kept chunk seq = %d, want 40 (oldest dropped)", chunks[0].Seq)
	}
	if ctl := chunks[10]; ctl.Stream != "control" || !ctl.Truncated || ctl.Seq != 50 {
		t.Errorf("control = %+v, want truncated with seq 50", ctl)
	}
}

// TestOutboxResumesFromSpool: a daemon that stops mid-delivery leaves a
// spool; the next one sends exactly what was not yet accepted.
func TestOutboxResumesFromSpool(t *testing.T) {
	srv := newDeviceServer(t)
	tr := newTestTransport(srv.URL)
	dir := t.TempDir()

	o := newOutbox(dir, testMeta("inv_resume"), tr.post, fastPolicy(), quiet)
	ctx, stop := context.WithCancel(context.Background())
	done := deliverAsync(ctx, o, noKick)
	o.output("stdout", "one\n")
	waitFor(t, func() bool { _, c, _ := srv.received(); return len(c) == 1 })

	srv.setDown(true)
	o.output("stdout", "two\n")
	o.output("stderr", "three\n")
	o.end(chunk{ExitCode: intPtr(1)})
	time.Sleep(50 * time.Millisecond)
	stop() // the daemon exits
	waitDelivered(t, done, false)

	files := spoolFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("spool files = %v, want the unfinished one", files)
	}
	again, err := loadOutbox(files[0], tr.post, fastPolicy(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	srv.setDown(false)
	waitDelivered(t, deliverAsync(context.Background(), again, noKick), true)

	acks, chunks, _ := srv.received()
	if len(acks) != 1 {
		t.Errorf("acks = %v, want the ack sent once", acks)
	}
	var got []string
	for _, c := range chunks {
		got = append(got, c.Stream+":"+strings.TrimSpace(c.Chunk))
	}
	want := "stdout:one stdout:two stderr:three control:"
	if strings.Join(got, " ") != want {
		t.Errorf("server got %v, want %s", got, want)
	}
	if ctl := chunks[len(chunks)-1]; ctl.Seq != 3 || ctl.ExitCode == nil || *ctl.ExitCode != 1 {
		t.Errorf("control = %+v", ctl)
	}
}

// TestOutboxCompactionKeepsState: a long-running command's journal is
// rewritten once it grows, and replaying it gives the same queue.
func TestOutboxCompactionKeepsState(t *testing.T) {
	srv := newDeviceServer(t)
	tr := newTestTransport(srv.URL)
	p := fastPolicy()
	p.CompactMin = 512
	dir := t.TempDir()

	o := newOutbox(dir, testMeta("inv_compact"), tr.post, p, quiet)
	ctx, stop := context.WithCancel(context.Background())
	done := deliverAsync(ctx, o, noKick)
	for i := 1; i <= 200; i++ {
		o.output("stdout", "line of output\n")
		waitFor(t, func() bool { _, c, _ := srv.received(); return len(c) == i })
	}
	srv.setDown(true)
	o.setPID(4242)
	o.output("stdout", "after\n")
	time.Sleep(30 * time.Millisecond)
	stop()
	waitDelivered(t, done, false)

	info, err := os.Stat(spoolFiles(t, dir)[0])
	if err != nil {
		t.Fatal(err)
	}
	// Uncompacted, 200 chunks and their acks take well over 10 KB.
	if info.Size() > 2*p.CompactMin {
		t.Errorf("journal is %d bytes, want it compacted below %d", info.Size(), 2*p.CompactMin)
	}
	again, err := loadOutbox(spoolFiles(t, dir)[0], tr.post, p, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.pending) != 1 || again.pending[0].Chunk != "after\n" || again.pending[0].Seq != 200 {
		t.Fatalf("pending after replay = %+v", again.pending)
	}
	if again.nextSeq != 201 || !again.ackSent || again.pid != 4242 {
		t.Errorf("replayed state: nextSeq %d ackSent %v pid %d", again.nextSeq, again.ackSent, again.pid)
	}
}

func TestOutboxGivesUpPastDeadline(t *testing.T) {
	srv := newDeviceServer(t)
	tr := newTestTransport(srv.URL)
	dir := t.TempDir()
	meta := testMeta("inv_late")
	meta.GiveUpAt = time.Now().Add(-time.Second).Unix()

	o := newOutbox(dir, meta, tr.post, fastPolicy(), quiet)
	o.end(chunk{ExitCode: intPtr(0)})
	waitDelivered(t, deliverAsync(context.Background(), o, noKick), false)
	if _, _, posts := srv.received(); posts != 0 {
		t.Errorf("posts = %d past the deadline, want 0", posts)
	}
	if files := spoolFiles(t, dir); len(files) != 0 {
		t.Errorf("spool kept past the deadline: %v", files)
	}
}

// TestOutboxKickRetriesAtOnce: once the daemon is back online it does not
// wait out a long backoff before reporting.
func TestOutboxKickRetriesAtOnce(t *testing.T) {
	srv := newDeviceServer(t)
	srv.setDown(true)
	tr := newTestTransport(srv.URL)
	p := fastPolicy()
	p.First, p.Max = time.Hour, time.Hour

	var mu sync.Mutex
	kick := make(chan struct{})
	kicked := func() <-chan struct{} { mu.Lock(); defer mu.Unlock(); return kick }

	o := newOutbox(t.TempDir(), testMeta("inv_kick"), tr.post, p, quiet)
	o.end(chunk{ExitCode: intPtr(0)})
	done := deliverAsync(context.Background(), o, kicked)
	waitFor(t, func() bool { _, _, n := srv.received(); return n >= 1 })

	srv.setDown(false)
	mu.Lock()
	close(kick)
	kick = make(chan struct{})
	mu.Unlock()
	waitDelivered(t, done, true)
}

// TestOutboxWithoutSpoolDir: when the spool cannot be written the outbox
// still delivers from memory.
func TestOutboxWithoutSpoolDir(t *testing.T) {
	srv := newDeviceServer(t)
	tr := newTestTransport(srv.URL)
	o := newOutbox("", testMeta("inv_mem"), tr.post, fastPolicy(), quiet)
	o.output("stdout", "x\n")
	o.end(chunk{ExitCode: intPtr(0)})
	waitDelivered(t, deliverAsync(context.Background(), o, noKick), true)
	if srv.control() == nil {
		t.Fatal("control not delivered")
	}
}

func TestOutboxEndIsIdempotent(t *testing.T) {
	o := newOutbox("", testMeta("inv_twice"), nil, fastPolicy(), quiet)
	o.end(chunk{ExitCode: intPtr(1)})
	o.end(chunk{ExitCode: intPtr(2)})
	o.output("stdout", "late\n")
	if len(o.pending) != 1 || *o.pending[0].ExitCode != 1 {
		t.Errorf("pending = %+v, want only the first control", o.pending)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
