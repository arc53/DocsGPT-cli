package target

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// httpClient is shared by all targets. It intentionally has no Timeout so the
// SSE stream and long polls are bounded only by the request context.
var httpClient = &http.Client{}

// truncateBody trims and rune-safely caps a response body for error messages.
func truncateBody(b []byte, max int) string {
	s := strings.TrimSpace(string(b))
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// extractToolCalls normalizes a raw JSON array of server tool_calls items,
// whose shapes vary between endpoints. Name is taken from the first present of
// "tool_name"/"name"/"action_name"; Arguments from "arguments"/"args"
// (stringified verbatim when it is a JSON object/array rather than a string).
// The same helper backs all three targets.
func extractToolCalls(rawArray json.RawMessage) []ToolCallInfo {
	if len(rawArray) == 0 {
		return nil
	}
	arr := gjson.ParseBytes(rawArray)
	if !arr.IsArray() {
		return nil
	}
	var out []ToolCallInfo
	arr.ForEach(func(_, item gjson.Result) bool {
		var info ToolCallInfo
		for _, k := range []string{"tool_name", "name", "action_name"} {
			if r := item.Get(k); r.Exists() && r.String() != "" {
				info.Name = r.String()
				break
			}
		}
		for _, k := range []string{"arguments", "args"} {
			r := item.Get(k)
			if !r.Exists() || r.Type == gjson.Null {
				continue
			}
			if r.Type == gjson.String {
				info.Arguments = r.String()
			} else {
				info.Arguments = r.Raw
			}
			break
		}
		out = append(out, info)
		return true
	})
	return out
}

// pollTaskStatus polls GET {baseURL}/api/task_status?task_id=... until the task
// reaches SUCCESS, returning the raw response body. FAILURE, context
// cancellation, or an unexpected HTTP status yield an error. A 503 is treated
// as transient and polling continues: the endpoint answers 503 whenever no
// idle Celery worker responds to its control ping, and a busy solo-pool
// worker — busy running our task — produces exactly that false negative.
// It is shared by the webhook target and attachment uploads.
func pollTaskStatus(ctx context.Context, baseURL, taskID string, interval time.Duration) ([]byte, error) {
	if interval <= 0 {
		interval = 2 * time.Second // defensive fallback; mirrors spec.DefaultPollInterval
	}
	statusURL := strings.TrimRight(baseURL, "/") + "/api/task_status?task_id=" + url.QueryEscape(taskID)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastState := "not yet polled"
	for {
		// Interval-first: every task_status hit triggers a Celery control ping
		// that competes with solo-pool workers for attention, so give the task
		// room to run before the first poll and between polls.
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("task_status %s: %w (last state: %s)", statusURL, ctx.Err(), lastState)
		case <-ticker.C:
		}

		body, status, err := getJSON(ctx, statusURL)
		if err != nil {
			return nil, err
		}
		if status == http.StatusServiceUnavailable {
			lastState = "503 no idle Celery workers (transient under solo pools)"
			continue
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("task_status %s returned %d: %s", statusURL, status, truncateBody(body, 300))
		}

		state := strings.ToUpper(gjson.GetBytes(body, "status").String())
		switch state {
		case "SUCCESS":
			return body, nil
		case "FAILURE", "FAILED", "REVOKED":
			return nil, fmt.Errorf("task %s failed: %s", taskID, truncateBody(body, 300))
		case "":
			state = "PENDING"
		}
		lastState = state
		// PENDING / STARTED / PROGRESS / RETRY -> keep polling.
	}
}

// redactWebhookURL hides the last path segment of a webhook URL — the secret
// token — so error messages are safe for CI logs.
func redactWebhookURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable webhook URL>"
	}
	if i := strings.LastIndex(u.Path, "/"); i >= 0 && i+1 < len(u.Path) {
		u.Path = u.Path[:i+1] + "..." // ASCII: url.String() would percent-encode "…"
	}
	u.RawQuery = ""
	return u.String()
}

// unwrapURLError strips the *url.Error layer, whose message embeds the full
// request URL (for webhooks that includes the secret token).
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}

// getJSON performs a context-aware GET and returns the body and status code.
func getJSON(ctx context.Context, target string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build GET %s: %w", target, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("GET %s: %w", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read %s body: %w", target, err)
	}
	return body, resp.StatusCode, nil
}
