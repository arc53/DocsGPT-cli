package docsgpt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastRetry is the default policy with waits short enough for tests.
func fastRetry(events *[]RetryEvent) RetryPolicy {
	p := DefaultRetryPolicy()
	p.BaseDelay = time.Millisecond
	p.OnRetry = func(ev RetryEvent) { *events = append(*events, ev) }
	return p
}

const sseAnswer = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func TestRetriesTransientFailuresThenSucceeds(t *testing.T) {
	for _, status := range []int{429, 502, 503, 504} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d stream=%v", status, stream), func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if calls.Add(1) <= 2 {
						w.WriteHeader(status)
						fmt.Fprint(w, "<html><body>bad gateway</body></html>")
						return
					}
					if stream {
						fmt.Fprint(w, sseAnswer)
						return
					}
					fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`)
				}))
				defer srv.Close()
				var events []RetryEvent
				c := NewClient(srv.URL, "k")
				c.Retry = fastRetry(&events)
				res, err := c.RunWithTools(context.Background(), []Message{{Role: "user", Content: "q"}}, RunOptions{Stream: stream})
				if err != nil {
					t.Fatal(err)
				}
				if got := res.Messages[len(res.Messages)-1].Content; got != "hi" {
					t.Fatalf("answer = %q", got)
				}
				if calls.Load() != 3 || len(events) != 2 {
					t.Fatalf("calls = %d, events = %d, want 3 and 2", calls.Load(), len(events))
				}
				if events[0].Attempt != 1 || events[1].Attempt != 2 || events[1].MaxRetries != 3 {
					t.Fatalf("events = %+v", events)
				}
				if events[1].Delay != 2*events[0].Delay {
					t.Fatalf("delays %v, %v: want them doubling", events[0].Delay, events[1].Delay)
				}
			})
		}
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	var events []RetryEvent
	c := NewClient(srv.URL, "k")
	c.Retry = fastRetry(&events)
	_, err := c.SendStream(context.Background(), ChatRequest{}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 502 {
		t.Fatalf("err = %v, want the 502", err)
	}
	if calls.Load() != 4 || len(events) != 3 {
		t.Fatalf("calls = %d, events = %d, want 4 and 3", calls.Load(), len(events))
	}
}

func TestDoesNotRetryFinalFailures(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"401": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) },
		"500": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
		"x-should-retry": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Should-Retry", "false")
			w.WriteHeader(429)
		},
		"limit reached": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"success":false,"message":"Daily limit reached","error_code":"free-limit-reached"}`)
		},
		"retry-after over the cap": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(429)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); h(w, r) }))
			defer srv.Close()
			var events []RetryEvent
			c := NewClient(srv.URL, "k")
			c.Retry = fastRetry(&events)
			if _, err := c.Send(context.Background(), ChatRequest{}); err == nil {
				t.Fatal("no error")
			}
			if calls.Load() != 1 || len(events) != 0 {
				t.Fatalf("calls = %d, events = %d, want no retry", calls.Load(), len(events))
			}
		})
	}
}

// TestMidStreamFailureIsNotRetried: once the answer has started, a cut
// connection is an error, never a second answer.
func TestMidStreamFailureIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"half\"}}]}\n\n")
		w.(http.Flusher).Flush()
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer srv.Close()
	var events []RetryEvent
	c := NewClient(srv.URL, "k")
	c.Retry = fastRetry(&events)
	var got strings.Builder
	_, err := c.SendStream(context.Background(), ChatRequest{}, func(d Delta, _ string) { got.WriteString(d.Content) })
	if err == nil {
		t.Fatal("a cut stream succeeded")
	}
	if calls.Load() != 1 || len(events) != 0 || got.String() != "half" {
		t.Fatalf("calls = %d, events = %d, answer %q: want one request", calls.Load(), len(events), got.String())
	}
}

func TestHonoursRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"}}]}`)
	}))
	defer srv.Close()
	var events []RetryEvent
	c := NewClient(srv.URL, "k")
	c.Retry = fastRetry(&events)
	start := time.Now()
	if _, err := c.Send(context.Background(), ChatRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Delay != time.Second {
		t.Fatalf("events = %+v, want one 1s wait", events)
	}
	if time.Since(start) < time.Second {
		t.Fatal("did not wait for Retry-After")
	}
}

func TestRetriesRefusedConnections(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	var events []RetryEvent
	c := NewClient(url, "k")
	c.Retry = fastRetry(&events)
	if _, err := c.Send(context.Background(), ChatRequest{}); err == nil {
		t.Fatal("no error")
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3 retries", len(events))
	}
}

func TestCancelDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c := NewClient(srv.URL, "k")
	c.Retry.BaseDelay = time.Hour
	c.Retry.MaxDelay = 2 * time.Hour
	c.Retry.OnRetry = func(RetryEvent) { time.AfterFunc(10*time.Millisecond, cancel) }
	start := time.Now()
	_, err := c.Send(ctx, ChatRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the wait was not cancelled")
	}
}

func TestZeroPolicyNeverRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTPClient: http.DefaultClient}
	c.Send(context.Background(), ChatRequest{})
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestAPIErrorMessage(t *testing.T) {
	for body, want := range map[string]string{
		`{"error":{"message":"Invalid API key","type":"auth_error"}}`: "API error 401: Invalid API key",
		`{"error":"bad key"}`:                    "API error 401: bad key",
		`{"success":false,"message":"Nope"}`:     "API error 401: Nope",
		"<html><title>401</title></html>":        "API error 401 Unauthorized",
		"plain text\nsecond line":                "API error 401: plain text second line",
		"":                                       "API error 401 Unauthorized",
		strings.Repeat("x", 300):                 "API error 401: " + strings.Repeat("x", 199) + "…",
		`{"error":{"code":"x","type":"server"}}`: "API error 401 Unauthorized",
	} {
		if got := (&APIError{StatusCode: 401, Body: body}).Error(); got != want {
			t.Errorf("body %.30q: Error() = %q, want %q", body, got, want)
		}
	}
	if got := (&APIError{Body: `{"error":{"code":"resume_in_progress","type":"conflict_error"}}`}).Code(); got != "resume_in_progress" {
		t.Errorf("Code() = %q", got)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Duration{
		"":                              0,
		"5":                             5 * time.Second,
		"-3":                            0,
		"soon":                          0,
		"Thu, 01 Jan 2026 00:00:30 GMT": 30 * time.Second,
		"Wed, 31 Dec 2025 23:00:00 GMT": 0,
	} {
		if got := retryAfter(v, now); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", v, got, want)
		}
	}
}

// TestBlockingOnRetryCountsTowardsTheWait: a countdown shown from OnRetry
// does not double the wait.
func TestBlockingOnRetryCountsTowardsTheWait(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"}}]}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "k")
	c.Retry.BaseDelay = 300 * time.Millisecond
	c.Retry.OnRetry = func(ev RetryEvent) { time.Sleep(ev.Delay) }
	start := time.Now()
	if _, err := c.Send(context.Background(), ChatRequest{}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 550*time.Millisecond {
		t.Fatalf("took %v, want about one 300ms wait", d)
	}
}
