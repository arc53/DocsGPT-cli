package manage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A manifest sync tells the server which files a source should hold (name
// and SHA-256 of each) and uploads only the ones it answers it lacks:
//
//	POST /api/manage_source_files  operation=sync_manifest, manifest        → needs_files [names]
//	POST /api/manage_source_files  operation=sync_manifest, manifest, files → needs_files | synced | unchanged
//
// Each uploaded file is named after its hash rather than its path, so a
// path in any script reaches the server intact (in the manifest's JSON) and
// one upload serves every file with that content. Files may arrive over
// several requests: the server stores what each one brings, and removes the
// files the manifest dropped and re-ingests only once it holds them all.

// syncManifestOp is its own operation, not a field of `sync`: a server
// without it refuses every request of one, where a `sync` it half understood
// would take a batch of changed files for the whole set and remove the rest.
const syncManifestOp = "sync_manifest"

// Manifest sync statuses.
const (
	syncNeedsFiles = "needs_files"
	syncSynced     = "synced"
	syncUnchanged  = "unchanged"
	// syncReplayed is ours, for the cached reply to an Idempotency-Key the
	// server has seen: an earlier sync's task, and nothing done now.
	syncReplayed = "replayed"
)

// CodeSourceFilesBusy is the 409 of a source whose files another request is
// changing.
const CodeSourceFilesBusy = "source_files_busy"

// errNoManifestSync marks a server that predates manifest syncs.
var errNoManifestSync = errors.New("the server does not support manifest syncs")

// manifestResult is the server's reply to a manifest sync request.
type manifestResult struct {
	Status       string   `json:"status"`
	Needed       []string `json:"needed"`
	TaskID       string   `json:"reingest_task_id"`
	SourceID     string   `json:"source_id"`
	Files        []string `json:"files"`
	RemovedFiles []string `json:"removed_files"` // stored paths
	Removed      []string `json:"removed"`       // the same files, as the client named them
	Uploaded     int      `json:"uploaded"`
	Unchanged    int      `json:"unchanged"`
}

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// postManifest sends the manifest of tree and the content of send (files of
// tree, one per hash).
func (c *Client) postManifest(ctx context.Context, sourceID string, tree, send []UploadFile, key string) (*manifestResult, error) {
	const path = "/api/manage_source_files"
	doc := struct {
		Version int            `json:"version"`
		Files   []manifestFile `json:"files"`
	}{Version: 1, Files: make([]manifestFile, len(tree))}
	for i, f := range tree {
		if f.SHA256 == "" {
			return nil, fmt.Errorf("sync: %s has no hash", f.Path)
		}
		doc.Files[i] = manifestFile{Path: f.name(), SHA256: f.SHA256}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	// A file part, not a form field: servers cap the size of form fields.
	parts := []filePart{{Field: "manifest", Filename: "manifest.json", Data: raw}}
	for _, f := range send {
		parts = append(parts, filePart{Field: "file", Filename: f.SHA256, Path: f.Path})
	}
	respBody, err := c.postFiles(ctx, path, [][2]string{{"source_id", sourceID}, {"operation", syncManifestOp}}, parts, key)
	if err != nil {
		// An older server refuses the operation by name, before it looks at
		// the source or the files: it can never take a batch for a whole set.
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusBadRequest && ae.Code == "" &&
			strings.Contains(ae.Message, "operation must be") {
			return nil, errNoManifestSync
		}
		return nil, err
	}
	var out manifestResult
	if err := decode(http.MethodPost, path, respBody, &out); err != nil {
		return nil, err
	}
	switch out.Status {
	case syncNeedsFiles, syncUnchanged:
	case syncSynced:
		if out.TaskID == "" {
			return nil, fmt.Errorf("POST %s: response has no reingest_task_id: %s", path, truncate(respBody, 300))
		}
	default:
		// A cached reply is the earlier task alone; an older server that took
		// the files as the whole set lists them.
		if out.Status == "" && out.TaskID != "" && out.Files == nil {
			out.Status = syncReplayed
			break
		}
		return nil, fmt.Errorf("POST %s: unexpected sync status %q: %s", path, out.Status, truncate(respBody, 300))
	}
	return &out, nil
}

// busyWait is how long a sync keeps retrying a source whose files another
// request is changing, when the run does not wait for ingestion.
const busyWait = 2 * time.Minute

// postManifestRetrying is postManifest, tried again while the source's files
// are busy with another request.
func (c *Client) postManifestRetrying(ctx context.Context, opts IngestOptions, sourceID string, send []UploadFile) (*manifestResult, error) {
	limit := busyWait
	if opts.Wait && opts.Timeout > 0 {
		limit = opts.Timeout
	}
	deadline := time.Now().Add(limit)
	delay := opts.Poll.Initial
	if delay <= 0 {
		delay = 2 * time.Second
	}
	for {
		res, err := c.postManifest(ctx, sourceID, opts.Files, send, opts.SyncKey)
		var ae *APIError
		if err == nil || !errors.As(err, &ae) || ae.Status != http.StatusConflict || ae.Code != CodeSourceFilesBusy {
			return res, err
		}
		if time.Now().Add(delay).After(deadline) {
			return nil, fmt.Errorf("the source's files were busy with another change for %s: %w", limit, err)
		}
		fmt.Fprintf(opts.Log, "another change to this source's files is running; trying again in %s\n", delay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay *= 2; delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
}

// syncManifest brings the source to opts.Files by manifest. It returns
// errNoManifestSync, having changed nothing, when the server predates them.
func (c *Client) syncManifest(ctx context.Context, target string, opts IngestOptions) (*UploadReport, error) {
	byName := make(map[string]UploadFile, len(opts.Files))
	for _, f := range opts.Files {
		byName[f.name()] = f
	}
	fmt.Fprintf(opts.Log, "comparing %d file(s) with %q (source %s)...\n", len(opts.Files), opts.Name, target)
	res, err := c.postManifestRetrying(ctx, opts, target, nil)
	if err != nil {
		return nil, err
	}
	uploaded := 0
	for res.Status == syncNeedsFiles {
		batch, err := needBatch(byName, res.Needed)
		if err != nil {
			return nil, err
		}
		left := len(res.Needed)
		fmt.Fprintf(opts.Log, "uploading %d of %d changed file(s) (%s)...\n", len(batch), left, byteSize(totalSize(batch)))
		if res, err = c.postManifestRetrying(ctx, opts, target, batch); err != nil {
			return nil, err
		}
		uploaded += res.Uploaded
		if res.Status == syncNeedsFiles && len(res.Needed) >= left {
			return nil, fmt.Errorf("the server still needs %d file(s) after an upload; the source may be changing under this sync, run it again", len(res.Needed))
		}
	}
	removed := res.Removed
	if len(removed) != len(res.RemovedFiles) {
		removed = res.RemovedFiles
	}
	for _, name := range removed {
		fmt.Fprintf(opts.Log, "removed %s\n", name)
	}
	report := &UploadReport{
		Name: opts.Name, TaskID: res.TaskID, SourceID: target, IdempotencyKey: opts.SyncKey,
		Synced: true, RemovedFiles: removed, Files: len(opts.Files), Uploaded: uploaded,
	}
	switch res.Status {
	case syncUnchanged:
		report.Unchanged, report.Status = true, "UNCHANGED"
		fmt.Fprintln(opts.Log, "the source already holds these files; nothing to ingest")
	case syncReplayed:
		report.Deduplicated = true
		fmt.Fprintln(opts.Log, "the server deduplicated this sync (same Idempotency-Key as an earlier request); no file was compared or sent")
	}
	return report, nil
}

// needBatch picks the files one request uploads for the names the server
// needs: one file per content, within the request limits.
func needBatch(byName map[string]UploadFile, needed []string) ([]UploadFile, error) {
	var batch []UploadFile
	var size int64
	sent := map[string]bool{}
	for _, name := range needed {
		f, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("the server asked for %q, which is not in this sync", name)
		}
		if sent[f.SHA256] {
			continue
		}
		if len(batch) > 0 && (len(batch) >= maxRequestFiles || size+f.Size > maxRequestBytes) {
			break
		}
		sent[f.SHA256] = true
		batch = append(batch, f)
		size += f.Size
	}
	if len(batch) == 0 {
		return nil, errors.New("the server needs files but named none")
	}
	return batch, nil
}

// syncStateMaxAge is how long a sync trusts its record without asking the
// server: a source changed or deleted there is noticed within a day.
const syncStateMaxAge = 24 * time.Hour

// syncState is the record of the last sync of one source name on one server
// with one token that left the server's index current: which files it sent. While the files
// stay the same, the next sync has nothing to do and stops before any request.
type syncState struct {
	path string // "" = no record kept

	Server   string    `json:"server"`
	Name     string    `json:"name"`
	SourceID string    `json:"source_id"`
	Tree     string    `json:"tree"`
	At       time.Time `json:"at"`
}

// syncState loads the record for this server, token and source name: another
// account's source of that name is another source. A missing or unreadable
// record is an empty one.
func (o IngestOptions) syncState(server, token string) *syncState {
	st := &syncState{}
	if o.StateDir == "" || !o.Sync || o.Whole {
		return st
	}
	sum := sha256.Sum256([]byte(server + "\x00" + token + "\x00" + strings.ToLower(o.Name)))
	path := filepath.Join(o.StateDir, hex.EncodeToString(sum[:16])+".json")
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, st) != nil || st.Server != server || !strings.EqualFold(st.Name, o.Name) {
			*st = syncState{}
		}
	}
	st.path, st.Server, st.Name = path, server, o.Name
	return st
}

// unchanged is the report of a sync that has nothing to do, or nil when the
// server has to be asked.
func (s *syncState) unchanged(o IngestOptions) *UploadReport {
	if s.path == "" || o.Force || s.SourceID == "" || s.Tree == "" || s.Tree != TreeHash(o.Files) {
		return nil
	}
	if age := time.Since(s.At); age < 0 || age > syncStateMaxAge {
		// The source may have been changed or deleted on the server since.
		return nil
	}
	fmt.Fprintf(o.Log, "nothing changed since the sync of %s; the server was not contacted (--force to ask it)\n", s.At.Local().Format("2006-01-02 15:04"))
	return &UploadReport{
		Name: o.Name, SourceID: s.SourceID, Synced: true, Files: len(o.Files),
		Unchanged: true, Local: true, Status: "UNCHANGED",
	}
}

// save records a sync the server confirmed: it reported the source
// unchanged, or its ingestion was waited for and succeeded. A sync that was
// only queued, or deduplicated, is not recorded, so the next run asks the
// server.
func (s *syncState) save(o IngestOptions, report *UploadReport) {
	if s.path == "" || report.SourceID == "" || report.Deduplicated {
		return
	}
	if !report.Unchanged && !(report.Waited && report.Status == "SUCCESS") {
		return
	}
	s.SourceID, s.Tree, s.At = report.SourceID, TreeHash(o.Files), time.Now().UTC()
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	// Best effort: without the record the next sync asks the server.
	if os.MkdirAll(filepath.Dir(s.path), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".sync-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(append(raw, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), s.path) != nil {
		os.Remove(tmp.Name())
	}
}
