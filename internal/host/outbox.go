package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// An outbox delivers one invocation's ack, output and exit code to the
// server. Chunks are queued in seq order and posted in batches by a single
// goroutine (deliver), retried with backoff until the server accepts them or
// no longer knows the invocation. Each change is first appended to a journal
// in the spool directory, so a daemon that stops (or crashes) before the
// server has everything resends the rest when it starts again.

// spoolExt names an invocation's journal: <invocation id>.ndjson.
const spoolExt = ".ndjson"

// deliveryPolicy bounds an outbox. Tests shrink it.
type deliveryPolicy struct {
	Budget      int           // stdout/stderr bytes kept unsent; past it the oldest chunks are dropped
	Batch       int           // most bytes sent in one POST (at least one chunk)
	First, Max  time.Duration // retry delay: First, doubling up to Max, with jitter
	PostTimeout time.Duration // one request
	CompactMin  int64         // journal size before it is first rewritten
}

func defaultPolicy() deliveryPolicy {
	return deliveryPolicy{
		Budget:      1 << 20,
		Batch:       256 << 10,
		First:       time.Second,
		Max:         time.Minute,
		PostTimeout: 30 * time.Second,
		CompactMin:  4 << 20,
	}
}

// backoff is the delay before retry number `attempt` (1-based): half of the
// doubled delay is fixed, the other half random.
func (p deliveryPolicy) backoff(attempt int) time.Duration {
	d := p.First
	for i := 1; i < attempt && d < p.Max; i++ {
		d *= 2
	}
	d = min(d, p.Max)
	return d/2 + rand.N(d/2+1)
}

// chunk is one NDJSON line of POST .../output.
type chunk struct {
	Stream     string `json:"stream"` // stdout, stderr or control
	Chunk      string `json:"chunk,omitempty"`
	Seq        int    `json:"seq"`
	ExitCode   *int   `json:"exit_code,omitempty"` // control only, always set there
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"` // output was dropped while it could not be sent
}

// spoolMeta is what an outbox needs to deliver after a restart.
type spoolMeta struct {
	InvocationID string `json:"invocation_id"`
	SessionID    string `json:"session_id"`
	DeviceID     string `json:"device_id"`
	GiveUpAt     int64  `json:"give_up_at"`         // Unix seconds
	Decision     string `json:"decision,omitempty"` // the ack to send first, if any
	Reason       string `json:"reason,omitempty"`
}

// record is one journal line. The first carries Meta; replaying the rest in
// order rebuilds the queue exactly (the budget drops the same chunks again).
type record struct {
	Meta      *spoolMeta `json:"meta,omitempty"`
	Chunk     *chunk     `json:"chunk,omitempty"`
	Acked     *int       `json:"acked,omitempty"` // the server has every chunk up to this seq
	AckSent   bool       `json:"ack_sent,omitempty"`
	PID       int        `json:"pid,omitempty"`
	NextSeq   int        `json:"next_seq,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
}

// sendFunc posts body to a server path and returns the HTTP status.
type sendFunc func(ctx context.Context, path string, body []byte) (int, error)

type outbox struct {
	meta   spoolMeta
	send   sendFunc
	policy deliveryPolicy
	logf   func(string)
	wake   chan struct{} // a token after each change; deliver waits on it

	mu        sync.Mutex
	path      string // the journal; "" when not journaling
	file      *os.File
	size      int64
	compactAt int64
	pending   []chunk // unaccepted, in seq order; a control chunk is last
	outBytes  int     // stdout/stderr bytes in pending
	nextSeq   int
	truncated bool
	ackSent   bool
	ended     bool // the control chunk is queued
	pid       int
}

var invocationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// newOutbox starts an outbox journaled under dir ("" keeps it in memory only).
func newOutbox(dir string, meta spoolMeta, send sendFunc, p deliveryPolicy, logf func(string)) *outbox {
	o := &outbox{
		meta:    meta,
		send:    send,
		policy:  p,
		logf:    logf,
		wake:    make(chan struct{}, 1),
		ackSent: meta.Decision == "",
	}
	if dir != "" && invocationIDPattern.MatchString(meta.InvocationID) {
		o.mu.Lock()
		o.path = filepath.Join(dir, meta.InvocationID+spoolExt)
		o.rewrite()
		o.mu.Unlock()
	}
	return o
}

// loadOutbox rebuilds an outbox from its journal.
func loadOutbox(path string, send sendFunc, p deliveryPolicy, logf func(string)) (*outbox, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	o := &outbox{send: send, policy: p, logf: logf, wake: make(chan struct{}, 1)}
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 16<<20)
	first := true
	for sc.Scan() {
		var rec record
		// A line torn by a crash mid-write is skipped.
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if first && rec.Meta == nil {
			return nil, errors.New("journal does not start with its metadata")
		}
		first = false
		o.apply(rec)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if o.meta.InvocationID == "" {
		return nil, errors.New("journal has no invocation id")
	}
	o.ackSent = o.ackSent || o.meta.Decision == ""
	o.mu.Lock()
	o.path = path
	o.rewrite()
	o.mu.Unlock()
	return o, nil
}

// apply replays one journal record onto the state. Caller holds mu (or owns o).
func (o *outbox) apply(rec record) {
	if rec.Meta != nil {
		o.meta = *rec.Meta
	}
	if rec.PID != 0 {
		o.pid = rec.PID
	}
	o.nextSeq = max(o.nextSeq, rec.NextSeq)
	o.truncated = o.truncated || rec.Truncated
	o.ackSent = o.ackSent || rec.AckSent
	if rec.Chunk != nil {
		o.queue(*rec.Chunk)
	}
	if rec.Acked != nil {
		o.accept(*rec.Acked)
	}
}

// queue appends c and keeps the unsent output within the budget by dropping
// the oldest stdout/stderr chunks. The control chunk is never dropped.
func (o *outbox) queue(c chunk) {
	if o.ended {
		return
	}
	o.pending = append(o.pending, c)
	o.nextSeq = max(o.nextSeq, c.Seq+1)
	if c.Stream == "control" {
		o.ended = true
		return
	}
	o.outBytes += len(c.Chunk)
	for o.outBytes > o.policy.Budget && len(o.pending) > 1 {
		o.outBytes -= len(o.pending[0].Chunk)
		o.pending[0] = chunk{}
		o.pending = o.pending[1:]
		o.truncated = true
	}
}

// accept drops every chunk up to seq: the server has them.
func (o *outbox) accept(seq int) {
	n := 0
	for n < len(o.pending) && o.pending[n].Seq <= seq {
		o.outBytes -= len(o.pending[n].Chunk)
		o.pending[n] = chunk{}
		n++
	}
	o.pending = o.pending[n:]
}

// output queues a stdout or stderr chunk.
func (o *outbox) output(stream, text string) {
	if text == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ended {
		return
	}
	c := chunk{Stream: stream, Chunk: text, Seq: o.nextSeq}
	o.queue(c)
	o.journal(record{Chunk: &c}, false)
	o.signal()
}

// end queues the closing control chunk; later output and ends are ignored.
func (o *outbox) end(c chunk) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ended {
		return
	}
	c.Stream = "control"
	c.Seq = o.nextSeq
	c.Truncated = c.Truncated || o.truncated
	o.queue(c)
	// Synced: the exit code is the one thing a restart must not lose.
	o.journal(record{Chunk: &c}, true)
	o.signal()
}

// setPID records the command's process id, for the report after a crash.
func (o *outbox) setPID(pid int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pid = pid
	o.journal(record{PID: pid}, false)
}

func (o *outbox) signal() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// journal appends rec, and rewrites the journal once it has doubled since
// the last rewrite. A write error stops journaling: the outbox carries on
// from memory and its file is removed so no stale journal is replayed.
func (o *outbox) journal(rec record, sync bool) {
	if o.file == nil {
		return
	}
	line, err := json.Marshal(rec)
	if err == nil {
		var n int
		n, err = o.file.Write(append(line, '\n'))
		o.size += int64(n)
	}
	if err == nil && sync {
		err = o.file.Sync()
	}
	if err != nil {
		o.stopJournal(err)
		return
	}
	if o.size > o.compactAt {
		o.rewrite()
	}
}

// rewrite replaces the journal with the current state (atomically, through a
// temporary file) and reopens it for appending.
func (o *outbox) rewrite() {
	if o.path == "" {
		return
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(record{Meta: &o.meta})
	_ = enc.Encode(record{PID: o.pid, NextSeq: o.nextSeq, Truncated: o.truncated, AckSent: o.ackSent})
	for i := range o.pending {
		_ = enc.Encode(record{Chunk: &o.pending[i]})
	}
	if o.file != nil {
		o.file.Close()
		o.file = nil
	}
	tmp := o.path + ".tmp"
	err := writeSynced(tmp, buf.Bytes())
	if err == nil {
		err = os.Rename(tmp, o.path)
	}
	if err == nil {
		o.file, err = os.OpenFile(o.path, os.O_WRONLY|os.O_APPEND, 0600)
	}
	if err != nil {
		os.Remove(tmp)
		o.stopJournal(err)
		return
	}
	o.size = int64(buf.Len())
	o.compactAt = max(o.policy.CompactMin, 2*o.size)
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (o *outbox) stopJournal(err error) {
	o.logf(fmt.Sprintf("%s: cannot write the spool (%v); its report is kept in memory only", o.meta.InvocationID, err))
	if o.file != nil {
		o.file.Close()
		o.file = nil
	}
	if o.path != "" {
		os.Remove(o.path)
		o.path = ""
	}
}

// finish closes the journal; remove deletes it (the report was delivered or
// will never be).
func (o *outbox) finish(remove bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.file != nil {
		o.file.Close()
		o.file = nil
	}
	if remove && o.path != "" {
		os.Remove(o.path)
	}
	o.path = ""
}

// delivery is one request deliver makes.
type delivery struct {
	path    string
	body    []byte
	ack     bool
	through int  // last seq in the batch
	final   bool // the batch ends with the control chunk
}

// next returns the next request: the ack first, then a batch of chunks.
func (o *outbox) next() (delivery, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	base := "/api/devices/sessions/" + o.meta.SessionID + "/invocations/" + o.meta.InvocationID
	if !o.ackSent {
		body, _ := json.Marshal(map[string]string{"decision": o.meta.Decision, "reason": o.meta.Reason})
		return delivery{path: base + "/ack", body: body, ack: true}, true
	}
	if len(o.pending) == 0 {
		return delivery{}, false
	}
	d := delivery{path: base + "/output"}
	var buf bytes.Buffer
	for i, c := range o.pending {
		line, _ := json.Marshal(c)
		if i > 0 && buf.Len()+len(line)+1 > o.policy.Batch {
			break
		}
		buf.Write(line)
		buf.WriteByte('\n')
		d.through, d.final = c.Seq, c.Stream == "control"
	}
	d.body = buf.Bytes()
	return d, true
}

// delivered records that the server accepted d.
func (o *outbox) delivered(d delivery) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if d.ack {
		o.ackSent = true
		o.journal(record{AckSent: true}, false)
		return
	}
	o.accept(d.through)
	o.journal(record{Acked: &d.through}, false)
}

// deliver sends everything queued, now and later, until the control chunk
// is accepted (true) or the report is given up (false): the server answered
// 404/410 (it no longer knows the invocation) or another client error, or
// the give-up time passed. It also returns false when ctx ends, leaving the
// journal for the next start. kicked returns a channel closed when the
// daemon reconnects, which cuts a retry delay short.
func (o *outbox) deliver(ctx context.Context, kicked func() <-chan struct{}) bool {
	id := o.meta.InvocationID
	attempt := 0
	for {
		kick := kicked()
		d, ok := o.next()
		if !ok {
			select {
			case <-o.wake:
				continue
			case <-ctx.Done():
				return false
			}
		}
		if time.Now().Unix() >= o.meta.GiveUpAt {
			o.logf(id + ": gave up reporting the result: the server did not take it in time")
			o.finish(true)
			return false
		}
		pctx, cancel := context.WithTimeout(ctx, o.policy.PostTimeout)
		status, err := o.send(pctx, d.path, d.body)
		cancel()
		if ctx.Err() != nil {
			return false
		}
		switch {
		case err == nil && status >= 200 && status < 300:
			attempt = 0
			o.delivered(d)
			if d.final {
				o.finish(true)
				return true
			}
			continue
		case err == nil && (status == http.StatusNotFound || status == http.StatusGone):
			o.logf(fmt.Sprintf("%s: the server no longer knows this command (HTTP %d); dropped its report", id, status))
			o.finish(true)
			return false
		case err == nil && status >= 400 && status < 500 && !retryableStatus(status):
			if d.ack {
				o.logf(fmt.Sprintf("%s: the server refused the ack (HTTP %d); sending the output anyway", id, status))
				o.delivered(d)
				continue
			}
			o.logf(fmt.Sprintf("%s: the server refused the report (HTTP %d); dropped it", id, status))
			o.finish(true)
			return false
		}
		attempt++
		select {
		case <-time.After(o.policy.backoff(attempt)):
		case <-kick:
			attempt = 0
		case <-ctx.Done():
			return false
		}
	}
}

// retryableStatus: client errors that can pass. A 401 is retried because
// the poller, not the outbox, decides the device was revoked.
func retryableStatus(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests
}

func intPtr(v int) *int { return &v }
