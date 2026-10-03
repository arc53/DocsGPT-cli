package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

func TestExplainChatError(t *testing.T) {
	for _, tc := range []struct {
		status int
		header string
		body   string
		want   []string
	}{
		{401, "", `{"error":{"message":"Invalid API key","type":"auth_error"}}`, []string{`rejected the API key "work"`, "401: Invalid API key", "docsgpt-cli login"}},
		{403, "", "", []string{"rejected the API key", "403 Forbidden"}},
		{404, "", "<html>nope</html>", []string{"no chat API at", "404 Not Found", "config set url"}},
		{429, "30", "", []string{"Rate limited by", "Try again in 30s"}},
		{502, "", "<html><body><h1>502 Bad Gateway</h1></body></html>", []string{"had a server error (502 Bad Gateway)"}},
		{400, "", `{"error":{"message":"messages field is required"}}`, []string{"refused the request (400: messages field is required)"}},
	} {
		apiErr := &docsgpt.APIError{StatusCode: tc.status, Body: tc.body}
		if tc.header != "" {
			apiErr.RetryAfter = 30 * time.Second
		}
		got := explainChatError(apiErr, "https://docs.example.com", "work").Error()
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%d: %q lacks %q", tc.status, got, w)
			}
		}
		if strings.Contains(got, "<html") || strings.Contains(got, "\n") {
			t.Errorf("%d: %q is not one clean line", tc.status, got)
		}
	}

	got := explainChatError(&docsgpt.APIError{StatusCode: 401}, "https://x", config.EnvAPIKey).Error()
	if !strings.Contains(got, "key in "+config.EnvAPIKey) {
		t.Errorf("env key: %q", got)
	}

	t.Setenv("DOCSGPT_DEBUG", "1")
	got = explainChatError(&docsgpt.APIError{StatusCode: 502, Body: "<html>raw</html>"}, "https://x", "k").Error()
	if !strings.Contains(got, "\n  HTTP 502: <html>raw</html>") {
		t.Errorf("DOCSGPT_DEBUG: %q", got)
	}
}

func TestExplainNetworkErrors(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	refused := srv.URL
	srv.Close()
	tlsSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsSrv.Close()
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()

	for _, tc := range []struct{ url, want string }{
		{refused, "connection refused. Is the server running at " + refused},
		{tlsSrv.URL, "TLS certificate is not trusted"},
		{strings.Replace(plain.URL, "http:", "https:", 1), "does not speak TLS"},
		{"http://no-such-host.invalid", "no such host"},
	} {
		c := docsgpt.NewClient(tc.url, "k")
		c.Retry = docsgpt.RetryPolicy{}
		_, err := c.Send(context.Background(), docsgpt.ChatRequest{})
		got := explainChatError(err, tc.url, "k").Error()
		if !strings.Contains(got, "Can't reach") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestRetryLabel(t *testing.T) {
	ev := docsgpt.RetryEvent{Attempt: 1, MaxRetries: 3, Delay: 4 * time.Second, Err: &docsgpt.APIError{StatusCode: 502}}
	if got := retryLabel(ev, 3200*time.Millisecond); got != "Retrying (1/3) in 4s… (502 Bad Gateway)" {
		t.Errorf("label = %q", got)
	}

	var shown []string
	ctx := context.Background()
	start := time.Now()
	countdown(ctx, docsgpt.RetryEvent{Delay: 2100 * time.Millisecond}, func(left time.Duration) {
		shown = append(shown, fmt.Sprint(int((left+time.Second-1)/time.Second)))
	})
	if d := time.Since(start); d < 2*time.Second || d > 2500*time.Millisecond {
		t.Errorf("countdown took %v", d)
	}
	if strings.Join(shown, ",") != "3,2,1" {
		t.Errorf("ticks = %v, want 3,2,1", shown)
	}

	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start = time.Now()
	countdown(ctx, docsgpt.RetryEvent{Delay: time.Hour}, func(time.Duration) {})
	if time.Since(start) > time.Second {
		t.Error("a cancelled countdown went on")
	}
}
