package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
)

// UsageError is a failure fixed by different flags or configuration rather
// than by retrying; the CLI exits 2 for it.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func usageErrorf(format string, args ...any) error {
	return &UsageError{Err: fmt.Errorf(format, args...)}
}

// ReadPayload reads a webhook payload from a file or stdin ("-") and checks
// it is JSON the webhook accepts: the server refuses a missing body and null.
func ReadPayload(file string, stdin io.Reader) ([]byte, error) {
	label := file
	var data []byte
	var err error
	if file == "-" {
		label = "stdin"
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(file)
	}
	if err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("payload from %s is empty: the webhook needs a JSON value", label)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("payload from %s is not valid JSON: %v", label, err)
	}
	if v == nil {
		return nil, fmt.Errorf("payload from %s is JSON null: the webhook needs a value (usually an object)", label)
	}
	return data, nil
}

// TriggerOptions are the inputs of Trigger. Exactly one of AgentID and
// WebhookURL is set.
type TriggerOptions struct {
	AgentID    string
	WebhookURL string
	Payload    []byte
	Key        string // Idempotency-Key; "" sends none
	Wait       bool
	Timeout    time.Duration
	Poll       WaitOptions // zero: TriggerPoll

	BaseURL   string // where the token is valid
	Token     string // personal access token; "" when none
	UserAgent string
	Log       io.Writer // progress lines
}

// TriggerPoll is the --wait cadence: agent runs finish in seconds to minutes,
// so the backoff tops out sooner than an ingest's.
var TriggerPoll = WaitOptions{Initial: time.Second, Max: 5 * time.Second}

// TriggerReport is the outcome of Trigger (and the `agents trigger --json`
// document).
type TriggerReport struct {
	TaskID         string          `json:"task_id"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	Deduplicated   bool            `json:"deduplicated,omitempty"`
	Waited         bool            `json:"waited"`
	Status         string          `json:"status,omitempty"`
	Answer         string          `json:"answer,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"` // the task's result, verbatim
	Error          string          `json:"error,omitempty"`

	ToolCalls int           `json:"-"` // of a finished run
	Elapsed   time.Duration `json:"-"` // spent waiting for the run
}

// Trigger resolves the agent's webhook (looking it up with the token when
// given an agent id), posts the payload and, with Wait, polls the run until
// it finishes. The report is nil when nothing was queued; otherwise it
// carries the error too. No error or log line ever contains the webhook
// token.
func Trigger(ctx context.Context, opts TriggerOptions) (report *TriggerReport, err error) {
	var authed *Client
	if opts.Token != "" {
		authed = New(opts.BaseURL, opts.Token, opts.UserAgent)
	}
	var hook *Webhook
	if opts.WebhookURL != "" {
		if hook, err = ParseWebhookURL(opts.WebhookURL); err != nil {
			return nil, usageErrorf("--webhook-url / %s: %w", config.EnvWebhookURL, err)
		}
	} else {
		if authed == nil {
			return nil, usageErrorf("no personal access token configured: an agent id is looked up with a token "+
				"(scope agents:keys): run 'docsgpt-cli login', set %s or pass --token — or pass --webhook-url", config.EnvToken)
		}
		if hook, err = authed.AgentWebhook(ctx, opts.AgentID); err != nil {
			return nil, fmt.Errorf("look up the webhook of agent %s: %w", opts.AgentID, err)
		}
	}
	// Belt and braces: whatever a server or transport error echoes, the
	// token in the webhook URL does not leave this function.
	defer func() {
		if err != nil {
			err = &redactedError{err: err, hook: hook}
			if report != nil {
				report.Error = err.Error()
			}
		}
	}()

	// The webhook and task_status are called without credentials.
	anon := New(hook.BaseURL(), "", opts.UserAgent)
	fmt.Fprintf(opts.Log, "triggering %s (%d-byte payload)...\n", hook, len(opts.Payload))
	res, err := anon.TriggerWebhook(ctx, hook, opts.Payload, opts.Key)
	if err != nil {
		return nil, err
	}
	report = &TriggerReport{TaskID: res.TaskID, IdempotencyKey: opts.Key}

	if res.TaskID == DeduplicatedTaskID {
		// The key matched an earlier request whose task record is gone: that
		// run already happened and there is nothing to poll.
		report.TaskID = ""
		report.Deduplicated = true
		fmt.Fprintln(opts.Log, "the server deduplicated this request (same Idempotency-Key as an earlier one); the agent did not run again")
		return report, nil
	}
	if !opts.Wait {
		fmt.Fprintf(opts.Log, "agent run queued as task %s; pass --wait to wait for the answer\n", res.TaskID)
		return report, nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	poll := opts.Poll
	if poll.Initial == 0 && poll.Max == 0 {
		poll.Initial, poll.Max = TriggerPoll.Initial, TriggerPoll.Max
	}
	started := time.Now()
	poll.OnUpdate = progressLogger(opts.Log, started, false)
	report.Waited = true
	st, err := waitForRun(waitCtx, anon, authed, hook, res.TaskID, poll, opts.Log)
	if st != nil {
		report.Status = st.Status
		report.Result = st.Result
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			err = fmt.Errorf("timed out after %s waiting for the agent run (task %s is still running server-side): %w", opts.Timeout, res.TaskID, err)
		}
		return report, err
	}
	run, err := AgentRunResult(res.TaskID, st)
	if err != nil {
		return report, err
	}
	report.Answer = run.Answer
	report.ToolCalls = run.ToolCalls
	report.Elapsed = time.Since(started)
	return report, nil
}

// waitForRun polls the run's task without credentials, as DocsGPT serves
// /api/task_status. If the server requires them anyway, it polls again with
// the personal access token, which is only ever sent to the configured base
// URL: never to a webhook host the token was not configured for.
func waitForRun(ctx context.Context, anon, authed *Client, hook *Webhook, taskID string, poll WaitOptions, log io.Writer) (*TaskStatus, error) {
	st, err := anon.WaitTask(ctx, taskID, poll)
	var ae *APIError
	if err == nil || !errors.As(err, &ae) || (ae.Status != 401 && ae.Status != 403) {
		return st, err
	}
	statusURL := hook.BaseURL() + "/api/task_status"
	switch {
	case authed == nil:
		return nil, usageErrorf("the agent run was queued as task %s, but %s requires authentication to report on it: "+
			"pass --token, set %s or run 'docsgpt-cli login' (the token needs chat:run, sources:read or sources:write)",
			taskID, statusURL, config.EnvToken)
	case !hook.SameOrigin(authed.BaseURL):
		return nil, usageErrorf("the agent run was queued as task %s, but %s requires authentication to report on it, "+
			"and the personal access token is only sent to %s: pass --url %s",
			taskID, statusURL, authed.BaseURL, hook.BaseURL())
	}
	fmt.Fprintln(log, "task status requires authentication; polling with the personal access token")
	return authed.WaitTask(ctx, taskID, poll)
}

// progressLogger returns a WaitOptions.OnUpdate that prints
// "  [  12s] STATUS" whenever the status line changes; withPercent appends
// the worker's progress figure to unfinished states.
func progressLogger(w io.Writer, started time.Time, withPercent bool) func(string, *TaskStatus) {
	last := ""
	return func(status string, task *TaskStatus) {
		line := status
		if withPercent && task != nil && status != "SUCCESS" {
			if pct, ok := task.Progress(); ok {
				line = fmt.Sprintf("%s %d%%", status, pct)
			}
		}
		if line == last {
			return
		}
		last = line
		fmt.Fprintf(w, "  [%4.0fs] %s\n", time.Since(started).Seconds(), line)
	}
}

// redactedError keeps the webhook token out of an error message while
// preserving the chain the exit code is read from.
type redactedError struct {
	err  error
	hook *Webhook
}

func (e *redactedError) Error() string { return e.hook.Redact(e.err.Error()) }
func (e *redactedError) Unwrap() error { return e.err }
