package host

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBatonStartsPolling(t *testing.T) {
	b := &Baton{}
	if b.State() != StatePolling {
		t.Fatalf("expected initial state polling, got %s", b.State())
	}
}

func TestBatonTransitionPollingToStreaming(t *testing.T) {
	b := &Baton{state: StatePolling}
	if !b.Transition(StatePolling, StateStreaming) {
		t.Fatal("expected first transition to succeed")
	}
	if b.State() != StateStreaming {
		t.Fatalf("expected streaming, got %s", b.State())
	}
}

func TestBatonTransitionRejectsWrongFrom(t *testing.T) {
	b := &Baton{state: StateStreaming}
	if b.Transition(StatePolling, StateStreaming) {
		t.Fatal("transition should reject when from-state doesn't match")
	}
}

func TestBatonTransitionStreamingToPolling(t *testing.T) {
	b := &Baton{state: StateStreaming}
	if !b.Transition(StateStreaming, StatePolling) {
		t.Fatal("expected reverse transition to succeed")
	}
	if b.State() != StatePolling {
		t.Fatalf("expected polling, got %s", b.State())
	}
}

func TestBatonSessionID(t *testing.T) {
	b := &Baton{}
	if b.SessionID() != "" {
		t.Fatal("expected empty session id initially")
	}
	b.SetSessionID("st_abc")
	if b.SessionID() != "st_abc" {
		t.Fatalf("expected session id 'st_abc', got %q", b.SessionID())
	}
}

func TestBatonStateString(t *testing.T) {
	if (StatePolling).String() != "polling" {
		t.Fatalf("polling stringer wrong")
	}
	if (StateStreaming).String() != "streaming" {
		t.Fatalf("streaming stringer wrong")
	}
}

// newTestTransport builds a Transport pointed at ``baseURL`` with a 5s
// poll interval (so the test never waits the full default).
func newTestTransport(baseURL string) *Transport {
	cfg := HostConfig{
		BaseURL:      baseURL,
		SessionToken: "tok_test",
		PollInterval: "5s",
	}
	return NewTransport(cfg, nil, "test")
}

// TestRunPollingFirstPollUnauthorizedReturnsRevoked verifies that an
// initial 401 (the daemon was never valid) is treated as revoked
// immediately. Otherwise a misconfigured token would poll forever.
func TestRunPollingFirstPollUnauthorizedReturnsRevoked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	tr := newTestTransport(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := tr.RunPolling(ctx, time.Time{})
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("expected ErrRevoked, got %v", err)
	}
}

// TestRunPollingSuccessThenUnauthorizedReturnsRevoked verifies that
// after a previously-successful poll, three consecutive 401s mean the
// device has been revoked and the loop bubbles ErrRevoked up.
func TestRunPollingSuccessThenUnauthorizedReturnsRevoked(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := HostConfig{
		BaseURL:      server.URL,
		SessionToken: "tok_test",
		// 5s is the floor enforced by PollIntervalDuration; the next-poll
		// sleep is bypassed by the backoff path for non-OK responses, but
		// the first OK response will still wait ``interval``. Set a short
		// fastUntil so the post-success poll uses fastInterval (5s floor).
		PollInterval: "5s",
	}
	tr := NewTransport(cfg, nil, "test")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := tr.RunPolling(ctx, time.Now().Add(60*time.Second))
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("expected ErrRevoked after revoke burst, got %v", err)
	}
	// Sanity: at least one OK (call 1) and ``authRejectThreshold`` 401s
	// after the OK before the loop gives up.
	if calls.Load() < int32(1+authRejectThreshold) {
		t.Fatalf("expected >= %d server calls, got %d",
			1+authRejectThreshold, calls.Load())
	}
}

// TestRunPollingTransient401TolerantWhileBelowThreshold verifies that
// a single 401 between successes (e.g. a redis blip during a token
// check) does not kill the poller. Two 401s + a recovery 202 + then
// the test cancels the context.
func TestRunPollingTransient401TolerantWhileBelowThreshold(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		switch n {
		case 1, 4:
			w.WriteHeader(http.StatusAccepted)
		case 2, 3:
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()

	tr := newTestTransport(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := tr.RunPolling(ctx, time.Now().Add(60*time.Second))
	// Either ctx.Err (we ran out the clock on the recovery side without
	// hitting the threshold) or ctx.Err via DeadlineExceeded — either
	// way, it must NOT be ErrRevoked.
	if errors.Is(err, ErrRevoked) {
		t.Fatalf("unexpected ErrRevoked under tolerant 401-burst, got %v", err)
	}
}

// TestPollOnceUnauthorizedReturnsSentinel verifies the lower-level
// PollOnce surfaces ErrAuthRejected so RunPolling can count consecutives.
func TestPollOnceUnauthorizedReturnsSentinel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	tr := newTestTransport(server.URL)
	_, _, err := tr.PollOnce(context.Background())
	if !errors.Is(err, ErrAuthRejected) {
		t.Fatalf("expected ErrAuthRejected, got %v", err)
	}
}

// TestRunSSEUnauthorizedReturnsRevoked verifies SSE that opens against
// a revoked session returns ErrRevoked (rather than a generic HTTP error).
func TestRunSSEUnauthorizedReturnsRevoked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	tr := newTestTransport(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := tr.RunSSE(ctx, "sess_test", "")
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("expected ErrRevoked, got %v", err)
	}
}

// TestRunSSERevokeEventReturnsRevoked verifies that an ``event: revoke``
// frame on the SSE stream triggers an ErrRevoked return.
func TestRunSSERevokeEventReturnsRevoked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("server does not support flushing")
		}
		fmt.Fprintf(w, "event: revoke\ndata: {}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	tr := newTestTransport(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := tr.RunSSE(ctx, "sess_test", "")
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("expected ErrRevoked on event: revoke, got %v", err)
	}
}
