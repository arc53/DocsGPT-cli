package manage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Source is one GET /api/sources entry.
type Source struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Date      any    `json:"date"`
	Tokens    any    `json:"tokens"`
	Type      string `json:"type"`
	Retriever string `json:"retriever"`
	Provider  string `json:"provider"`
	Ownership string `json:"ownership"`
}

// ListSources calls GET /api/sources (scope sources:read).
func (c *Client) ListSources(ctx context.Context) ([]Source, json.RawMessage, error) {
	var out []Source
	raw, err := c.getJSON(ctx, "/api/sources", nil, &out)
	return out, raw, err
}

// DeleteSource calls GET /api/delete_old?source_id= (scope sources:write). The
// server exposes the deletion as a GET.
func (c *Client) DeleteSource(ctx context.Context, id string) error {
	_, err := c.getJSON(ctx, "/api/delete_old", url.Values{"source_id": {id}}, nil)
	return err
}

// IdempotencyKeyMaxLen mirrors the server's Idempotency-Key limit.
const IdempotencyKeyMaxLen = 256

// DeduplicatedTaskID is the task_id sentinel the server returns when an
// Idempotency-Key repeat matched a request whose task record is gone: the
// original ingest already ran, and there is nothing left to poll.
const DeduplicatedTaskID = "deduplicated"

// UploadResult is the POST /api/upload reply.
type UploadResult struct {
	Success  bool   `json:"success"`
	TaskID   string `json:"task_id"`
	SourceID string `json:"source_id,omitempty"`
}

// uploadUserField is the legacy `user` form field. The server requires it to
// be present but derives the real owner from the token.
const uploadUserField = "local"

// UploadSource calls POST /api/upload (scope sources:write) with a multipart
// body: `user`, `name` and one `file` part per path. idempotencyKey, when
// non-empty, is sent as the Idempotency-Key header: a repeat within the
// server's dedup window returns the original task instead of re-ingesting.
// File contents are streamed, so large files are never buffered in memory.
func (c *Client) UploadSource(ctx context.Context, name string, paths []string, idempotencyKey string) (*UploadResult, error) {
	const path = "/api/upload"
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("upload: a source name is required")
	}
	if len(paths) == 0 {
		return nil, errors.New("upload: at least one file is required")
	}
	if len(idempotencyKey) > IdempotencyKeyMaxLen {
		return nil, fmt.Errorf("upload: idempotency key exceeds %d characters", IdempotencyKeyMaxLen)
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("upload: %w", err)
		}
		if st.IsDir() {
			return nil, fmt.Errorf("upload: %s is a directory (pass files, or a .zip archive)", p)
		}
	}

	ctx, cancel := withTimeout(ctx, c.UploadTimeout, DefaultUploadTimeout)
	defer cancel()

	body, contentType, length, err := uploadBody(name, paths)
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	defer body.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(path, nil), body)
	if err != nil {
		return nil, fmt.Errorf("build POST %s: %w", path, err)
	}
	// An explicit length avoids chunked transfer encoding, which not every
	// WSGI/ASGI front end accepts for multipart bodies.
	req.ContentLength = length
	req.Header.Set("Content-Type", contentType)
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	respBody, _, err := c.send(req, path)
	if err != nil {
		return nil, err
	}
	var out UploadResult
	if err := decode(http.MethodPost, path, respBody, &out); err != nil {
		return nil, err
	}
	if out.TaskID == "" {
		return nil, fmt.Errorf("POST %s: response has no task_id: %s", path, truncate(respBody, 300))
	}
	return &out, nil
}

// uploadBody assembles the multipart body as a chain of in-memory part
// headers and lazily opened files, so its exact length is known up front and
// file contents are streamed rather than buffered.
func uploadBody(name string, paths []string) (body io.ReadCloser, contentType string, length int64, err error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err = mw.WriteField("user", uploadUserField); err != nil {
		return nil, "", 0, err
	}
	if err = mw.WriteField("name", name); err != nil {
		return nil, "", 0, err
	}
	chain := &readerChain{}
	flush := func() {
		seg := append([]byte(nil), buf.Bytes()...)
		buf.Reset()
		length += int64(len(seg))
		chain.parts = append(chain.parts, func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(seg)), nil
		})
	}
	for _, p := range paths {
		st, statErr := os.Stat(p)
		if statErr != nil {
			return nil, "", 0, statErr
		}
		if _, err = mw.CreateFormFile("file", filepath.Base(p)); err != nil {
			return nil, "", 0, err
		}
		flush() // field parts + this file's part header
		p := p
		length += st.Size()
		chain.parts = append(chain.parts, func() (io.ReadCloser, error) { return os.Open(p) })
	}
	if err = mw.Close(); err != nil {
		return nil, "", 0, err
	}
	flush() // closing boundary
	return chain, mw.FormDataContentType(), length, nil
}

// readerChain reads a sequence of lazily opened readers back to back.
type readerChain struct {
	parts []func() (io.ReadCloser, error)
	cur   io.ReadCloser
}

func (r *readerChain) Read(p []byte) (int, error) {
	for {
		if r.cur == nil {
			if len(r.parts) == 0 {
				return 0, io.EOF
			}
			next, err := r.parts[0]()
			if err != nil {
				return 0, err
			}
			r.parts = r.parts[1:]
			r.cur = next
		}
		n, err := r.cur.Read(p)
		if err == io.EOF {
			r.cur.Close()
			r.cur = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (r *readerChain) Close() error {
	r.parts = nil
	if r.cur != nil {
		err := r.cur.Close()
		r.cur = nil
		return err
	}
	return nil
}

// DeriveIdempotencyKey builds a deterministic Idempotency-Key from the source
// name and the SHA-256 of every file (paired with its base name, order
// independent). The same name + content always yields the same key, so a CI
// retry of an upload is deduplicated server-side; any change to the name, a
// filename or a byte of content yields a new key.
func DeriveIdempotencyKey(name string, paths []string) (string, error) {
	entries := make([]string, 0, len(paths))
	for _, p := range paths {
		sum, err := hashFile(p)
		if err != nil {
			return "", err
		}
		entries = append(entries, filepath.Base(p)+"\x00"+sum)
	}
	sort.Strings(entries)
	h := sha256.New()
	io.WriteString(h, "docsgpt-cli/upload/v1\x00")
	io.WriteString(h, name)
	for _, e := range entries {
		io.WriteString(h, "\x00")
		io.WriteString(h, e)
	}
	return "docsgpt-cli-upload-" + hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// TaskStatus is the GET /api/task_status reply.
type TaskStatus struct {
	Status string          `json:"status"` // PENDING | STARTED | PROGRESS | RETRY | SUCCESS | FAILURE | REVOKED
	Result json.RawMessage `json:"result"`
}

// Progress extracts a 0-100 completion figure from a PROGRESS result, when
// the worker reports one.
func (t *TaskStatus) Progress() (int, bool) {
	var meta struct {
		Current *float64 `json:"current"`
	}
	if len(t.Result) == 0 || json.Unmarshal(t.Result, &meta) != nil || meta.Current == nil {
		return 0, false
	}
	return int(*meta.Current), true
}

// ResultText renders the task result for an error message.
func (t *TaskStatus) ResultText() string {
	if len(t.Result) == 0 || string(t.Result) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(t.Result, &s) == nil {
		return s
	}
	return truncate(t.Result, 300)
}

// GetTaskStatus calls GET /api/task_status?task_id= (any of sources:read,
// sources:write or chat:run).
func (c *Client) GetTaskStatus(ctx context.Context, taskID string) (*TaskStatus, error) {
	var out TaskStatus
	if _, err := c.getJSON(ctx, "/api/task_status", url.Values{"task_id": {taskID}}, &out); err != nil {
		return nil, err
	}
	out.Status = strings.ToUpper(out.Status)
	return &out, nil
}

// TaskFailedError is returned by WaitTask when the task ends in FAILURE or
// REVOKED.
type TaskFailedError struct {
	TaskID string
	Status string
	Detail string
}

func (e *TaskFailedError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("task %s ended with status %s", e.TaskID, e.Status)
	}
	return fmt.Sprintf("task %s ended with status %s: %s", e.TaskID, e.Status, e.Detail)
}

// WaitOptions tunes WaitTask. Zero values pick the defaults.
type WaitOptions struct {
	Initial time.Duration // first delay; default 1s
	Max     time.Duration // backoff ceiling; default 10s
	Factor  float64       // backoff multiplier; default 1.5
	// OnUpdate is called after every poll with the latest state. For a
	// transient 503 (no idle worker answered the server's ping, typical while
	// a solo worker is busy with this very task) status is "WAITING".
	OnUpdate func(status string, task *TaskStatus)
}

// WaitTask polls GET /api/task_status with exponential backoff until the task
// reaches SUCCESS (returned), FAILURE/REVOKED (*TaskFailedError) or ctx ends
// (the timeout). A 503 is transient and polling continues.
func (c *Client) WaitTask(ctx context.Context, taskID string, opts WaitOptions) (*TaskStatus, error) {
	delay := opts.Initial
	if delay <= 0 {
		delay = time.Second
	}
	max := opts.Max
	if max <= 0 {
		max = 10 * time.Second
	}
	if max < delay {
		max = delay
	}
	factor := opts.Factor
	if factor < 1 {
		factor = 1.5
	}

	last := "not yet polled"
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		// Delay first: the ingest task needs a moment to be picked up, and
		// every status call makes the server ping its workers.
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for task %s: %w (last status: %s)", taskID, ctx.Err(), last)
		case <-timer.C:
		}

		st, err := c.GetTaskStatus(ctx, taskID)
		switch {
		case err == nil:
			if st.Status == "" {
				st.Status = "PENDING"
			}
			last = st.Status
			if opts.OnUpdate != nil {
				opts.OnUpdate(st.Status, st)
			}
			switch st.Status {
			case "SUCCESS":
				return st, nil
			case "FAILURE", "FAILED", "REVOKED":
				return st, &TaskFailedError{TaskID: taskID, Status: st.Status, Detail: st.ResultText()}
			}
		case isStatus(err, http.StatusServiceUnavailable):
			last = "WAITING"
			if opts.OnUpdate != nil {
				opts.OnUpdate(last, nil)
			}
		case ctx.Err() != nil:
			return nil, fmt.Errorf("waiting for task %s: %w (last status: %s)", taskID, ctx.Err(), last)
		default:
			return nil, err
		}

		delay = time.Duration(float64(delay) * factor)
		if delay > max {
			delay = max
		}
		timer.Reset(delay)
	}
}

func isStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}
