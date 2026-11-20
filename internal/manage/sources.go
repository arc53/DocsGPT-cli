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
	"io/fs"
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

// UploadFile is one file of an upload: where it is on disk and the name it
// is sent under. A name with folders ("guide/intro.md") keeps that tree in
// the source, as the folders of a .zip archive do.
type UploadFile struct {
	Path   string // on disk
	Name   string // sent as the part's filename, "/"-separated; "" = the base name of Path
	Size   int64  // bytes, as CollectFiles saw it
	SHA256 string // lowercase hex, once HashFiles has run
}

func (f UploadFile) name() string {
	if f.Name != "" {
		return f.Name
	}
	return filepath.Base(f.Path)
}

// UploadSource calls POST /api/upload (scope sources:write) with a multipart
// body: `user`, `name` and one `file` part per file. idempotencyKey, when
// non-empty, is sent as the Idempotency-Key header: a repeat within the
// server's dedup window returns the original task instead of re-ingesting.
// File contents are streamed, so large files are never buffered in memory.
func (c *Client) UploadSource(ctx context.Context, name string, files []UploadFile, idempotencyKey string) (*UploadResult, error) {
	const path = "/api/upload"
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("upload: a source name is required")
	}
	if len(files) == 0 {
		return nil, errors.New("upload: at least one file is required")
	}
	if len(idempotencyKey) > IdempotencyKeyMaxLen {
		return nil, fmt.Errorf("upload: idempotency key exceeds %d characters", IdempotencyKeyMaxLen)
	}
	respBody, err := c.postFiles(ctx, path, [][2]string{{"user", uploadUserField}, {"name", name}}, fileParts(files), idempotencyKey)
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

// SyncResult is the POST /api/manage_source_files reply to a sync.
type SyncResult struct {
	Success      bool     `json:"success"`
	TaskID       string   `json:"reingest_task_id"`
	SourceID     string   `json:"source_id,omitempty"`
	Files        []string `json:"files,omitempty"`
	RemovedFiles []string `json:"removed_files,omitempty"`
}

// SyncSource calls POST /api/manage_source_files with operation=sync (scope
// sources:write): the files (and .zip archives, unpacked with their folders)
// become the source's whole file set, files missing from them are removed,
// and the server re-ingests the source, re-embedding only what changed. The
// source keeps its id. idempotencyKey, when non-empty, is sent as the
// Idempotency-Key header.
func (c *Client) SyncSource(ctx context.Context, sourceID string, files []UploadFile, idempotencyKey string) (*SyncResult, error) {
	const path = "/api/manage_source_files"
	if strings.TrimSpace(sourceID) == "" {
		return nil, errors.New("sync: a source id is required")
	}
	if len(files) == 0 {
		return nil, errors.New("sync: at least one file is required")
	}
	if len(idempotencyKey) > IdempotencyKeyMaxLen {
		return nil, fmt.Errorf("sync: idempotency key exceeds %d characters", IdempotencyKeyMaxLen)
	}
	respBody, err := c.postFiles(ctx, path, [][2]string{{"source_id", sourceID}, {"operation", "sync"}}, fileParts(files), idempotencyKey)
	if err != nil {
		return nil, err
	}
	var out SyncResult
	if err := decode(http.MethodPost, path, respBody, &out); err != nil {
		return nil, err
	}
	if out.TaskID == "" {
		return nil, fmt.Errorf("POST %s: response has no reingest_task_id: %s", path, truncate(respBody, 300))
	}
	return &out, nil
}

// filePart is one file part of a multipart body: a file on disk, or Data.
type filePart struct {
	Field    string // form field name
	Filename string
	Path     string // file on disk, streamed
	Data     []byte // in-memory content, when Path is ""
}

// fileParts are the `file` parts of an upload, each under its file's name.
func fileParts(files []UploadFile) []filePart {
	parts := make([]filePart, len(files))
	for i, f := range files {
		parts[i] = filePart{Field: "file", Filename: f.name(), Path: f.Path}
	}
	return parts
}

// postFiles POSTs a multipart body of fields and file parts, with the upload
// timeout. File contents are streamed, never buffered.
func (c *Client) postFiles(ctx context.Context, path string, fields [][2]string, parts []filePart, idempotencyKey string) ([]byte, error) {
	for _, part := range parts {
		if part.Path == "" {
			continue
		}
		st, err := os.Stat(part.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("upload: %s: no such file", part.Path)
		}
		if err != nil {
			return nil, fmt.Errorf("upload: %w", err)
		}
		if st.IsDir() {
			return nil, fmt.Errorf("upload: %s is a directory", part.Path)
		}
	}

	ctx, cancel := withTimeout(ctx, c.UploadTimeout, DefaultUploadTimeout)
	defer cancel()

	body, contentType, length, err := multipartBody(fields, parts)
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
	return c.send(req, path)
}

// multipartBody assembles the multipart body as a chain of in-memory part
// headers and lazily opened files, so its exact length is known up front and
// file contents are streamed rather than buffered.
func multipartBody(fields [][2]string, parts []filePart) (body io.ReadCloser, contentType string, length int64, err error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, field := range fields {
		if err = mw.WriteField(field[0], field[1]); err != nil {
			return nil, "", 0, err
		}
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
	for _, part := range parts {
		w, createErr := mw.CreateFormFile(part.Field, part.Filename)
		if createErr != nil {
			return nil, "", 0, createErr
		}
		if part.Path == "" {
			if _, err = w.Write(part.Data); err != nil {
				return nil, "", 0, err
			}
			continue
		}
		st, statErr := os.Stat(part.Path)
		if statErr != nil {
			return nil, "", 0, statErr
		}
		flush() // field parts + this file's part header
		p, size := part.Path, st.Size()
		length += size
		// Exactly the size the length was computed from: a file that grew
		// meanwhile is cut there, and one that shrank ends the body early
		// (an error), rather than either corrupting the parts after it.
		chain.parts = append(chain.parts, func() (io.ReadCloser, error) {
			f, openErr := os.Open(p)
			if openErr != nil {
				return nil, openErr
			}
			return &limitedFile{Reader: io.LimitReader(f, size), Closer: f}, nil
		})
	}
	if err = mw.Close(); err != nil {
		return nil, "", 0, err
	}
	flush() // closing boundary
	return chain, mw.FormDataContentType(), length, nil
}

// limitedFile reads at most a fixed number of bytes of a file.
type limitedFile struct {
	io.Reader
	io.Closer
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
// name and the SHA-256 of every file (paired with the name it is sent under,
// order independent). The same name + content always yields the same key, so
// a CI retry of an upload is deduplicated server-side; any change to the
// name, a file's name or folder, or a byte of content yields a new key. Files
// already hashed are not read again.
func DeriveIdempotencyKey(name string, files []UploadFile) (string, error) {
	entries := make([]string, 0, len(files))
	for _, f := range files {
		sum := f.SHA256
		if sum == "" {
			var err error
			if sum, err = hashFile(f.Path); err != nil {
				return "", err
			}
		}
		entries = append(entries, f.name()+"\x00"+sum)
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

// IngestOptions are the inputs of Ingest.
type IngestOptions struct {
	Name string
	// Files to upload (see CollectFiles). A sync that sends a manifest needs
	// them hashed (HashFiles).
	Files []UploadFile
	Key   string // Idempotency-Key; "" sends none
	// SyncKey is the Idempotency-Key of a Sync request; "" sends none. It is
	// never the upload's derived key: the server scopes keys per user, not per
	// endpoint, and a content key would replay an earlier sync after a revert.
	SyncKey string
	Wait    bool
	Timeout time.Duration
	// Replace deletes the caller's older sources with the same name once the
	// new one is ingested, so agents that reference the source by name pick
	// up the new content on their next apply. Needs Wait.
	Replace bool
	// Sync updates the caller's own source with this name in place instead of
	// uploading a new one; with none, it uploads.
	Sync bool
	// Whole sends every file even when the server could be told which ones it
	// already holds: .zip archives, which only the server can unpack, and file
	// arguments that share a name.
	Whole bool
	// StateDir keeps, per server and source name, what the last sync that
	// left the index current sent; a sync of the same files then stops before
	// any request. "" keeps nothing.
	StateDir string
	// Force contacts the server even when StateDir says nothing changed.
	Force bool
	Poll  WaitOptions // zero: WaitTask defaults
	Log   io.Writer   // progress lines
}

// UploadReport is the outcome of Ingest (and the `sources upload --json`
// document).
type UploadReport struct {
	Name           string   `json:"name"`
	TaskID         string   `json:"task_id"`
	SourceID       string   `json:"source_id,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	Deduplicated   bool     `json:"deduplicated,omitempty"`
	Waited         bool     `json:"waited"`
	Status         string   `json:"status,omitempty"`
	Replaced       []string `json:"replaced,omitempty"`
	Synced         bool     `json:"synced,omitempty"`
	RemovedFiles   []string `json:"removed_files,omitempty"`
	// Files is how many files the source holds after a sync; Uploaded how
	// many of them this run sent.
	Files    int `json:"files,omitempty"`
	Uploaded int `json:"uploaded_files,omitempty"`
	// Unchanged: the source already held these files and its index was
	// current, so nothing was ingested. Local: that was decided from the last
	// sync's record on this machine, without asking the server.
	Unchanged bool   `json:"unchanged,omitempty"`
	Local     bool   `json:"local,omitempty"`
	Error     string `json:"error,omitempty"`
}

// One request of a manifest sync, which can take several, carries at most
// this many files and bytes. The server reads at most 1000 parts and (by
// default) 256 MiB per request.
var (
	maxRequestFiles       = 500
	maxRequestBytes int64 = 200 << 20
)

// firstBatch is the longest prefix of files one request can carry: always at
// least one file.
func firstBatch(files []UploadFile) []UploadFile {
	var size int64
	for i, f := range files {
		if i > 0 && (i >= maxRequestFiles || size+f.Size > maxRequestBytes) {
			return files[:i]
		}
		size += f.Size
	}
	return files
}

// Ingest uploads the files as a new source, or with Sync brings the caller's
// source of that name up to date, and with Wait polls the server's task. The
// report is nil when the first request failed; otherwise it carries the
// error too.
func (c *Client) Ingest(ctx context.Context, opts IngestOptions) (report *UploadReport, err error) {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	state := opts.syncState(c.BaseURL, c.Token)
	if report := state.unchanged(opts); report != nil {
		return report, nil
	}

	report, rest, err := c.startIngest(ctx, opts)
	if report == nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			report.Error = err.Error()
		}
	}()

	if report.TaskID == DeduplicatedTaskID {
		// The key matched an earlier request whose task record is gone: that
		// ingest already ran, there is nothing to poll.
		report.Deduplicated = true
		if rest {
			return report, fmt.Errorf("the server deduplicated the upload of the first %d of %d files (same Idempotency-Key as an earlier request) and named no source to send the others to", report.Files, len(opts.Files))
		}
		fmt.Fprintln(opts.Log, "the server deduplicated this upload (same Idempotency-Key as an earlier request); nothing new was ingested")
		return report, c.replaceOlderSources(ctx, opts, report)
	}
	if report.Unchanged {
		state.save(opts, report)
		return report, nil
	}
	if !opts.Wait {
		fmt.Fprintf(opts.Log, "ingestion queued; poll it with --wait or check the web app (task %s)\n", report.TaskID)
		return report, nil
	}
	if err := c.waitIngest(ctx, opts, report); err != nil {
		return report, err
	}
	if rest {
		// The source exists now, with the files one request could carry; a
		// manifest sync brings the others.
		next, err := c.syncManifest(ctx, report.SourceID, opts)
		if errors.Is(err, errNoManifestSync) {
			err = fmt.Errorf("%d files do not fit one upload and this server cannot take them in several; upload a .zip archive, or update the server", len(opts.Files))
		}
		if err != nil {
			return report, err
		}
		next.IdempotencyKey = report.IdempotencyKey
		next.Uploaded += report.Uploaded
		*report = *next
		if !report.Unchanged {
			if err := c.waitIngest(ctx, opts, report); err != nil {
				return report, err
			}
		}
	}
	state.save(opts, report)
	return report, c.replaceOlderSources(ctx, opts, report)
}

// waitIngest polls the report's task until it ends.
func (c *Client) waitIngest(ctx context.Context, opts IngestOptions, report *UploadReport) error {
	waitCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	poll := opts.Poll
	poll.OnUpdate = progressLogger(opts.Log, time.Now(), true)
	report.Waited = true
	st, err := c.WaitTask(waitCtx, report.TaskID, poll)
	if st != nil {
		report.Status = st.Status
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			err = fmt.Errorf("timed out after %s waiting for ingestion (task %s is still running server-side; re-run with the same files to resume waiting): %w", opts.Timeout, report.TaskID, err)
		}
		return err
	}
	if reason, skipped := st.skipped(); skipped {
		// The task ended without touching the index: the source was busy
		// with another ingest, or is gone.
		return fmt.Errorf("the server skipped this ingestion (%s); run the same command again", reason)
	}
	return nil
}

// skipped reports a finished task whose result says it did not run.
func (t *TaskStatus) skipped() (reason string, ok bool) {
	var result struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if len(t.Result) == 0 || json.Unmarshal(t.Result, &result) != nil || result.Status != "skipped" {
		return "", false
	}
	if result.Reason == "" {
		result.Reason = "no reason given"
	}
	return result.Reason, true
}

// startIngest makes the run's first change: it uploads the files as a new
// source, or with Sync updates the caller's own source of that name in
// place. The report is nil when the request itself failed. rest is true when
// a new source was created from the first files only and the others are
// still to be synced into it: only a manifest sync that waits for the source
// can do that, any other upload is one request.
func (c *Client) startIngest(ctx context.Context, opts IngestOptions) (report *UploadReport, rest bool, err error) {
	if opts.Sync {
		target, err := c.syncTarget(ctx, opts.Name)
		if err != nil {
			return nil, false, err
		}
		if target != "" {
			report, err := c.syncInto(ctx, target, opts)
			return report, false, err
		}
		fmt.Fprintf(opts.Log, "no source named %q yet; creating it\n", opts.Name)
	}
	files, hint := opts.Files, "use --sync --wait on a folder, which sends them in several"
	switch {
	case !opts.Sync || opts.Whole:
	case opts.Wait:
		files = firstBatch(opts.Files)
	default:
		hint = "add --wait, so the others can follow once the source exists"
	}
	if len(files) < len(opts.Files) {
		fmt.Fprintf(opts.Log, "uploading the first %d of %d file(s) as %q...\n", len(files), len(opts.Files), opts.Name)
	} else {
		fmt.Fprintf(opts.Log, "uploading %d file(s) as %q...\n", len(files), opts.Name)
	}
	key := opts.Key
	if len(files) < len(opts.Files) && key != "" {
		// The key names what this request carries, not the whole folder.
		if key, err = DeriveIdempotencyKey(opts.Name, files); err != nil {
			return nil, false, err
		}
	}
	res, err := c.UploadSource(ctx, opts.Name, files, key)
	if err != nil {
		return nil, false, tooLarge(err, files, hint)
	}
	return &UploadReport{
		Name: opts.Name, TaskID: res.TaskID, SourceID: res.SourceID, IdempotencyKey: key,
		Files: len(files), Uploaded: len(files),
	}, len(files) < len(opts.Files), nil
}

// syncInto updates the source in place: by manifest, so only the files it
// lacks are sent, or (Whole, or a server without manifests) with every file.
func (c *Client) syncInto(ctx context.Context, target string, opts IngestOptions) (*UploadReport, error) {
	if !opts.Whole {
		report, err := c.syncManifest(ctx, target, opts)
		if !errors.Is(err, errNoManifestSync) {
			return report, err
		}
		fmt.Fprintln(opts.Log, "this server takes a sync only as a whole; sending every file")
	}
	fmt.Fprintf(opts.Log, "syncing %d file(s) into %q (source %s)...\n", len(opts.Files), opts.Name, target)
	res, err := c.SyncSource(ctx, target, opts.Files, opts.SyncKey)
	if err != nil {
		if !opts.Whole {
			err = tooLarge(err, opts.Files, "upload a .zip archive, or update the server")
		}
		return nil, err
	}
	for _, removed := range res.RemovedFiles {
		fmt.Fprintf(opts.Log, "removed %s\n", removed)
	}
	return &UploadReport{
		Name: opts.Name, TaskID: res.TaskID, SourceID: target, IdempotencyKey: opts.SyncKey,
		Synced: true, RemovedFiles: res.RemovedFiles, Files: len(opts.Files), Uploaded: len(opts.Files),
	}, nil
}

// tooLarge adds, to the server's refusal (413) of one request with more
// files than a batch holds, how they can be sent.
func tooLarge(err error, files []UploadFile, hint string) error {
	if !isStatus(err, http.StatusRequestEntityTooLarge) || len(firstBatch(files)) == len(files) {
		return err
	}
	return fmt.Errorf("%d files (%s) do not fit one upload; %s: %w", len(files), byteSize(totalSize(files)), hint, err)
}

func totalSize(files []UploadFile) (n int64) {
	for _, f := range files {
		n += f.Size
	}
	return n
}

// byteSize renders a size for a message: "12 KB", "3.4 MB".
func byteSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// syncTarget is the id of the caller's own source named name (compared
// case-insensitively, as Replace does), or "" when there is none. Several
// such sources are an error: which one agents read is not obvious, and
// syncing one would leave the others stale.
func (c *Client) syncTarget(ctx context.Context, name string) (string, error) {
	sources, _, err := c.ListSources(ctx)
	if err != nil {
		return "", fmt.Errorf("--sync: list sources: %w", err)
	}
	var ids []string
	for _, src := range sources {
		if !strings.EqualFold(src.Name, name) || (src.Ownership != "" && src.Ownership != "user") {
			continue
		}
		ids = append(ids, src.ID)
	}
	switch len(ids) {
	case 0:
		return "", nil
	case 1:
		return ids[0], nil
	}
	return "", fmt.Errorf("--sync: you have %d sources named %q (%s); delete all but one, then sync again", len(ids), name, strings.Join(ids, ", "))
}

// replaceOlderSources implements Replace. The server resolves an agent's
// source by name and picks the oldest match, so without this every upload of
// changed content leaves agents pinned to the first upload. Only the caller's
// own sources are touched, never the one just uploaded, and nothing is deleted
// unless the server said which source that is.
func (c *Client) replaceOlderSources(ctx context.Context, opts IngestOptions, report *UploadReport) error {
	if !opts.Replace {
		return nil
	}
	if report.SourceID == "" {
		fmt.Fprintln(opts.Log, "--replace skipped: the server did not report the new source id")
		return nil
	}
	sources, _, err := c.ListSources(ctx)
	if err != nil {
		return fmt.Errorf("--replace: list sources: %w", err)
	}
	// The id the server reported must be a source that exists right now. After a
	// revert to earlier content the Idempotency-Key repeats and, within the
	// server's dedup window, the cached reply names the source of that earlier
	// upload, which a later --replace may already have deleted. Deleting "every
	// other" source would then remove the only live one.
	live := false
	for _, src := range sources {
		if src.ID == report.SourceID {
			live = true
			break
		}
	}
	if !live {
		return fmt.Errorf("--replace: the server reported source %s, which no longer exists (this content was uploaded before and the request was deduplicated); nothing was deleted. Re-run with a fresh --idempotency-key to ingest it again", report.SourceID)
	}
	for _, src := range sources {
		if src.ID == report.SourceID || !strings.EqualFold(src.Name, opts.Name) {
			continue
		}
		if src.Ownership != "" && src.Ownership != "user" {
			continue
		}
		if err := c.DeleteSource(ctx, src.ID); err != nil {
			return fmt.Errorf("--replace: delete older source %s: %w", src.ID, err)
		}
		report.Replaced = append(report.Replaced, src.ID)
		fmt.Fprintf(opts.Log, "replaced older source %s\n", src.ID)
	}
	return nil
}
