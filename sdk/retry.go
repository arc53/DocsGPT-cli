package docsgpt

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// RetryPolicy says how a chat request that failed before any of its answer
// arrived is sent again: on 408, 429, 502, 503 and 504, and when the server
// could not be reached or dropped the connection before replying. A failure
// once the answer has started streaming is never retried, so an answer is
// never given twice.
type RetryPolicy struct {
	// MaxRetries is how many times a request is sent again; 0 never.
	MaxRetries int
	// BaseDelay is the wait before the first retry, doubled for each next
	// one; a Retry-After from the server takes its place.
	BaseDelay time.Duration
	// MaxDelay caps a wait. When the server asks for a longer one, the
	// error is returned at once.
	MaxDelay time.Duration
	// OnRetry, when set, is called before each wait. The wait still ends
	// Delay after the call, however long OnRetry takes, so it may block to
	// count the wait down.
	OnRetry func(RetryEvent)
}

// RetryEvent describes a retry about to happen.
type RetryEvent struct {
	Attempt    int           // 1 for the first retry
	MaxRetries int           // the policy's MaxRetries
	Delay      time.Duration // the wait before it
	Err        error         // why the last attempt failed
}

// DefaultRetryPolicy is what NewClient sets: 3 retries after 2s, 4s and 8s,
// waiting at most a minute.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxRetries: 3, BaseDelay: 2 * time.Second, MaxDelay: time.Minute}
}

// do sends the request newReq builds and returns a 200 response. Any other
// status becomes an *APIError. Failures before a response, and transient
// statuses, are retried as c.Retry says; a wait cancelled by ctx returns
// ctx's error.
func (c *Client) do(ctx context.Context, newReq func() (*http.Request, error)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		resp, err := c.HTTPClient.Do(req)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				return resp, nil
			}
			err = newAPIError(resp)
		}
		if ctx.Err() != nil {
			return nil, err
		}
		delay, ok := c.Retry.delay(attempt, err)
		if !ok {
			return nil, err
		}
		deadline := time.Now().Add(delay)
		if c.Retry.OnRetry != nil {
			c.Retry.OnRetry(RetryEvent{Attempt: attempt + 1, MaxRetries: c.Retry.MaxRetries, Delay: delay, Err: err})
		}
		t := time.NewTimer(time.Until(deadline))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// delay is the wait before retry number attempt+1 after err, and whether
// to retry at all.
func (p RetryPolicy) delay(attempt int, err error) (time.Duration, bool) {
	if attempt >= p.MaxRetries || !Retryable(err) {
		return 0, false
	}
	// Nothing listening is more often a wrong URL than a restart: one try.
	if attempt > 0 && errors.Is(err, syscall.ECONNREFUSED) {
		return 0, false
	}
	d := p.BaseDelay << attempt
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		d = apiErr.RetryAfter
		if p.MaxDelay > 0 && d > p.MaxDelay {
			return 0, false
		}
	}
	if p.MaxDelay > 0 && d > p.MaxDelay {
		d = p.MaxDelay
	}
	return d, true
}

// Retryable reports whether err, from a request that got no answer, is
// worth sending the request again for: a transient status (408, 429, 502,
// 503, 504) that the server did not mark final, or a connection that
// failed or was dropped. A spent usage limit (a 429 whose code ends in
// "limit-reached"), a name that does not resolve and a TLS failure are not.
func Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.noRetry {
			return false
		}
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			return !strings.HasSuffix(apiErr.Code(), "limit-reached")
		case http.StatusRequestTimeout, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// TLS reports a handshake alert as a "remote error" / "local error".
	if opErr := (*net.OpError)(nil); errors.As(err, &opErr) {
		return !strings.HasSuffix(opErr.Op, " error")
	}
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
