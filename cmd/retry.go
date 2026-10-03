package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// retrying returns a copy of c for one run whose retry waits count down on
// show ("Retrying (1/3) in 4s…"), then show "Thinking…" again. With the
// retry setting off it never retries.
func retrying(ctx context.Context, c *docsgpt.Client, s config.Settings, show func(string), hint string) *docsgpt.Client {
	cp := *c
	if s.Retry == "off" {
		cp.Retry.MaxRetries = 0
	}
	cp.Retry.OnRetry = func(ev docsgpt.RetryEvent) {
		countdown(ctx, ev, func(left time.Duration) { show(retryLabel(ev, left) + hint) })
		if ctx.Err() == nil {
			show("Thinking…")
		}
	}
	return &cp
}

// countdown calls tick with the time left of ev's wait, once a second,
// until it is over or ctx ends.
func countdown(ctx context.Context, ev docsgpt.RetryEvent, tick func(left time.Duration)) {
	end := time.Now().Add(ev.Delay)
	for {
		left := time.Until(end)
		if left <= 0 {
			return
		}
		tick(left)
		next := left % time.Second
		if next < 10*time.Millisecond {
			next += time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(min(next, left)):
		}
	}
}

// retryLabel says that a retry comes in left, and why.
func retryLabel(ev docsgpt.RetryEvent, left time.Duration) string {
	secs := int((left + time.Second - 1) / time.Second)
	return fmt.Sprintf("Retrying (%d/%d) in %ds… (%s)", ev.Attempt, ev.MaxRetries, secs, failureReason(ev.Err))
}

// failureReason names a failure in a few words: "502 Bad Gateway".
func failureReason(err error) string {
	var apiErr *docsgpt.APIError
	var netErr net.Error
	switch {
	case errors.As(err, &apiErr):
		return fmt.Sprintf("%d %s", apiErr.StatusCode, http.StatusText(apiErr.StatusCode))
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timed out"
	}
	return "connection failed"
}
