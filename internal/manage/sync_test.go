package manage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSources is a server with one user's sources, speaking /api/sources,
// /api/upload, /api/manage_source_files (sync, with or without manifests)
// and /api/task_status.
type fakeSources struct {
	t  *testing.T
	mu sync.Mutex

	noManifests bool // an older server: a sync is the whole file set
	busy        int  // answer this many manifest requests with 409
	replay      bool // a sync's Idempotency-Key is known: the cached reply, nothing done
	dedupUpload bool // an upload's key is known and its task record is gone
	maxFiles    int  // > 0: a request with more file parts is a 413
	taskResult  string

	sources  map[string]*fakeSource // by id
	requests []string               // "upload a.md,b.md", "ask", "send <sha>…", "whole a.md"
	nextID   int
}

type fakeSource struct {
	name    string
	files   map[string]string // name -> sha256, as recorded
	current bool              // the index reflects files
}

func newFakeSources(t *testing.T) *fakeSources {
	return &fakeSources{t: t, sources: map[string]*fakeSource{}, taskResult: `{"status":"completed"}`}
}

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// rawFilename is a part's filename as sent: Go's own parser keeps only its
// last element.
func rawFilename(header string) string {
	_, params, _ := mime.ParseMediaType(header)
	return params["filename"]
}

func (f *fakeSources) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/api/sources":
		type row struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		rows := []row{}
		for id, src := range f.sources {
			rows = append(rows, row{id, src.name})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
		raw, _ := json.Marshal(rows)
		writeJSONResp(w, 200, string(raw))
	case "/api/task_status":
		writeJSONResp(w, 200, `{"status":"SUCCESS","result":`+f.taskResult+`}`)
		for _, src := range f.sources {
			src.current = true
		}
	case "/api/upload":
		files := f.parts(r, "file")
		if f.dedupUpload {
			f.requests = append(f.requests, "upload (deduplicated)")
			writeJSONResp(w, 200, `{"success":true,"task_id":"deduplicated"}`)
			return
		}
		if f.maxFiles > 0 && len(files) > f.maxFiles {
			writeJSONResp(w, 413, `{"success":false,"message":"Upload exceeds the request limit"}`)
			return
		}
		f.nextID++
		id := "src-" + string(rune('0'+f.nextID))
		src := &fakeSource{name: r.FormValue("name"), files: map[string]string{}}
		var sent []string
		for _, p := range files {
			src.files[p.name] = sha(p.content)
			sent = append(sent, p.name)
		}
		f.sources[id] = src
		f.requests = append(f.requests, "upload "+strings.Join(sent, ","))
		writeJSONResp(w, 200, `{"success":true,"task_id":"task-up","source_id":"`+id+`"}`)
	case "/api/manage_source_files":
		f.sync(w, r)
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}
}

type sentPart struct{ name, content string }

func (f *fakeSources) parts(r *http.Request, field string) []sentPart {
	if r.MultipartForm == nil {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			f.t.Fatalf("parse multipart: %v", err)
		}
	}
	var out []sentPart
	for _, fh := range r.MultipartForm.File[field] {
		file, _ := fh.Open()
		body, _ := io.ReadAll(file)
		file.Close()
		out = append(out, sentPart{rawFilename(fh.Header.Get("Content-Disposition")), string(body)})
	}
	return out
}

func (f *fakeSources) sync(w http.ResponseWriter, r *http.Request) {
	files := f.parts(r, "file")
	manifests := f.parts(r, "manifest")
	src := f.sources[r.FormValue("source_id")]
	op := r.FormValue("operation")
	if op != "sync" && (op != "sync_manifest" || f.noManifests) {
		writeJSONResp(w, 400, `{"success":false,"message":"operation must be 'add', 'remove', 'remove_directory', or 'sync'"}`)
		return
	}
	if src == nil {
		writeJSONResp(w, 404, `{"success":false,"message":"Source not found"}`)
		return
	}
	if f.replay {
		f.requests = append(f.requests, "replay")
		writeJSONResp(w, 200, `{"success":true,"reingest_task_id":"task-old","source_id":"`+r.FormValue("source_id")+`"}`)
		return
	}
	if f.maxFiles > 0 && len(files) > f.maxFiles {
		writeJSONResp(w, 413, `{"success":false,"message":"Upload exceeds the request limit"}`)
		return
	}
	if op == "sync" {
		if len(files) == 0 {
			writeJSONResp(w, 400, `{"success":false,"message":"No files provided for sync operation"}`)
			return
		}
		src.files = map[string]string{}
		var sent []string
		for _, p := range files {
			src.files[p.name] = sha(p.content)
			sent = append(sent, p.name)
		}
		src.current = false
		f.requests = append(f.requests, "whole "+strings.Join(sent, ","))
		writeJSONResp(w, 200, `{"success":true,"reingest_task_id":"task-whole","source_id":"`+r.FormValue("source_id")+`"}`)
		return
	}
	if f.busy > 0 {
		f.busy--
		writeJSONResp(w, 409, `{"success":false,"code":"source_files_busy","message":"busy"}`)
		return
	}
	var doc struct {
		Version int
		Files   []struct{ Path, SHA256 string }
	}
	if err := json.Unmarshal([]byte(manifests[0].content), &doc); err != nil || doc.Version != 1 {
		writeJSONResp(w, 400, `{"success":false,"code":"bad_manifest","message":"bad manifest"}`)
		return
	}
	uploaded := map[string]bool{}
	var sent []string
	for _, p := range files {
		if sha(p.content) != p.name {
			writeJSONResp(w, 400, `{"success":false,"code":"bad_manifest","message":"An uploaded file does not match its sha256"}`)
			return
		}
		uploaded[p.name] = true
		sent = append(sent, p.name[:8])
	}
	if len(files) == 0 {
		f.requests = append(f.requests, "ask")
	} else {
		f.requests = append(f.requests, "send "+strings.Join(sent, ","))
	}
	var needed []string
	wrote, kept := 0, 0
	for _, entry := range doc.Files {
		switch {
		case src.files[entry.Path] == entry.SHA256:
			kept++
		case uploaded[entry.SHA256]:
			src.files[entry.Path] = entry.SHA256
			src.current = false
			wrote++
		default:
			needed = append(needed, entry.Path)
		}
	}
	reply := map[string]any{"success": true, "source_id": r.FormValue("source_id"), "uploaded": wrote, "unchanged": kept}
	switch {
	case len(needed) > 0:
		reply["status"], reply["needed"] = "needs_files", needed
	default:
		wanted := map[string]bool{}
		for _, entry := range doc.Files {
			wanted[entry.Path] = true
		}
		removed := []string{}
		for name := range src.files {
			if !wanted[name] {
				delete(src.files, name)
				removed = append(removed, name)
				src.current = false
			}
		}
		sort.Strings(removed)
		reply["removed_files"] = removed
		if src.current {
			reply["status"] = "unchanged"
		} else {
			reply["status"], reply["reingest_task_id"] = "synced", "task-sync"
		}
	}
	raw, _ := json.Marshal(reply)
	writeJSONResp(w, 200, string(raw))
}

func (f *fakeSources) log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.requests, " ; ")
}

func (f *fakeSources) stored(id string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for name, sum := range f.sources[id].files {
		out[name] = sum
	}
	return out
}

// syncFolder runs one `sources upload <dir> --sync --wait`.
func syncFolder(t *testing.T, c *Client, dir, stateDir string, mod func(*IngestOptions)) (*UploadReport, string, error) {
	t.Helper()
	found, err := CollectFiles([]string{dir}, CollectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := HashFiles(found.Files); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	opts := IngestOptions{
		Name: "Docs", Files: found.Files, Sync: true, Wait: true, Timeout: 5 * time.Second,
		StateDir: stateDir, Log: &log,
		Poll: WaitOptions{Initial: time.Millisecond, Max: 2 * time.Millisecond},
	}
	if mod != nil {
		mod(&opts)
	}
	report, err := c.Ingest(context.Background(), opts)
	return report, log.String(), err
}

func wantStored(t *testing.T, fake *fakeSources, id string, files map[string]string) {
	t.Helper()
	want := map[string]string{}
	for name, content := range files {
		want[name] = sha(content)
	}
	got := fake.stored(id)
	if len(got) != len(want) {
		t.Fatalf("stored = %v, want %v", got, want)
	}
	for name, sum := range want {
		if got[name] != sum {
			t.Fatalf("stored[%s] = %s, want %s (all: %v)", name, got[name], sum, got)
		}
	}
}

func TestSyncFolderLifecycle(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir, state := t.TempDir(), t.TempDir()
	tree := map[string]string{"README.md": "readme", "документы/введение.md": "вступление", "文档/指南.md": "指南"}
	writeTree(t, dir, tree)

	// No source yet: one upload, each file under its path in the folder.
	report, log, err := syncFolder(t, c, dir, state, nil)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, log)
	}
	if fake.log() != "upload README.md,документы/введение.md,文档/指南.md" {
		t.Fatalf("requests = %s", fake.log())
	}
	if report.SourceID != "src-1" || report.Status != "SUCCESS" || report.Unchanged {
		t.Errorf("report = %+v", report)
	}
	wantStored(t, fake, "src-1", tree)

	// Nothing changed: the record of that sync answers, no request at all.
	report, log, err = syncFolder(t, c, dir, state, nil)
	if err != nil || !report.Unchanged || !report.Local || report.SourceID != "src-1" {
		t.Fatalf("second run: %+v, %v", report, err)
	}
	if !strings.Contains(log, "nothing changed") || fake.log() != "upload README.md,документы/введение.md,文档/指南.md" {
		t.Errorf("the second run must not contact the server: %s\n%s", fake.log(), log)
	}

	// --force asks the server, which holds every file.
	report, _, err = syncFolder(t, c, dir, state, func(o *IngestOptions) { o.Force = true })
	if err != nil || !report.Unchanged || report.Local || report.TaskID != "" {
		t.Fatalf("forced run: %+v, %v", report, err)
	}
	if !strings.HasSuffix(fake.log(), " ; ask") {
		t.Errorf("requests = %s", fake.log())
	}

	// An edit, a new file and a removal: only the two changed files travel.
	tree["文档/指南.md"] = "指南 v2"
	tree["文档/新.md"] = "新"
	delete(tree, "README.md")
	writeTree(t, dir, tree)
	removeFile(t, filepath.Join(dir, "README.md"))
	before := len(fake.requests)
	report, log, err = syncFolder(t, c, dir, state, nil)
	if err != nil {
		t.Fatalf("change: %v\n%s", err, log)
	}
	got := strings.Join(fake.requests[before:], " ; ")
	want := "ask ; send " + sha("指南 v2")[:8] + "," + sha("新")[:8]
	if got != want {
		t.Errorf("requests = %s, want %s", got, want)
	}
	if report.Uploaded != 2 || report.Files != 3 || strings.Join(report.RemovedFiles, ",") != "README.md" ||
		report.TaskID != "task-sync" || report.Status != "SUCCESS" || !report.Synced {
		t.Errorf("report = %+v", report)
	}
	wantStored(t, fake, "src-1", tree)

	// And the record follows: the next run is silent again.
	report, _, _ = syncFolder(t, c, dir, state, nil)
	if !report.Local {
		t.Errorf("the run after a change should stop locally: %+v", report)
	}
}

func TestSyncFolderRecordIsOnlyKeptForAConfirmedSync(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir, state := t.TempDir(), t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "a"})
	noWait := func(o *IngestOptions) { o.Wait = false }

	// Queued, not waited for: nothing says the ingestion worked.
	if _, _, err := syncFolder(t, c, dir, state, noWait); err != nil {
		t.Fatal(err)
	}
	report, _, err := syncFolder(t, c, dir, state, noWait)
	if err != nil || report.Local {
		t.Fatalf("an unconfirmed sync must be checked with the server: %+v, %v", report, err)
	}
	// The server now says the source is current (the fake marks it so when
	// its task is polled; here it never was): a reingest is queued again.
	if !strings.HasSuffix(fake.log(), " ; ask") || report.TaskID == "" {
		t.Errorf("requests = %s, report %+v", fake.log(), report)
	}

	// A skipped reingest is a failure, and leaves no record either.
	fake.taskResult = `{"status":"skipped","reason":"busy"}`
	_, _, err = syncFolder(t, c, dir, state, nil)
	if err == nil || !strings.Contains(err.Error(), "skipped this ingestion (busy)") {
		t.Fatalf("err = %v", err)
	}
	fake.taskResult = `{"status":"completed"}`
	fake.sources["src-1"].current = false
	report, _, err = syncFolder(t, c, dir, state, nil)
	if err != nil || report.Local || report.Status != "SUCCESS" {
		t.Fatalf("after a skipped reingest: %+v, %v", report, err)
	}
	report, _, _ = syncFolder(t, c, dir, state, nil)
	if !report.Local {
		t.Errorf("a waited, successful sync is recorded: %+v", report)
	}
}

func TestSyncFolderRecordExpiresAndIsPerSource(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir, state := t.TempDir(), t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "a"})
	if _, _, err := syncFolder(t, c, dir, state, nil); err != nil {
		t.Fatal(err)
	}

	// Another source name has its own record.
	report, _, err := syncFolder(t, c, dir, state, func(o *IngestOptions) { o.Name = "Other" })
	if err != nil || report.Local || report.SourceID != "src-2" {
		t.Fatalf("another name: %+v, %v", report, err)
	}

	// A day-old record is not trusted: the source may be gone.
	paths, _ := filepath.Glob(filepath.Join(state, "*.json"))
	if len(paths) != 2 {
		t.Fatalf("records = %v", paths)
	}
	for _, p := range paths {
		var st syncState
		raw := readFile(t, p)
		if err := json.Unmarshal(raw, &st); err != nil {
			t.Fatal(err)
		}
		st.At = time.Now().Add(-25 * time.Hour)
		out, _ := json.Marshal(st)
		writeTemp(t, filepath.Dir(p), filepath.Base(p), string(out))
	}
	report, _, err = syncFolder(t, c, dir, state, nil)
	if err != nil || report.Local || !report.Unchanged {
		t.Fatalf("an old record: %+v, %v", report, err)
	}

	// No state directory: every run asks.
	report, _, _ = syncFolder(t, c, dir, "", nil)
	if report.Local {
		t.Errorf("without a state directory nothing is local: %+v", report)
	}
}

func TestSyncFolderSendsEachContentOnceInBatches(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"seed.md": "seed"})
	if _, _, err := syncFolder(t, c, dir, "", nil); err != nil {
		t.Fatal(err)
	}
	tree := map[string]string{"seed.md": "seed", "a.md": "same", "copy/a.md": "same", "b.md": "bb", "c.md": "cc", "d.md": "dd"}
	writeTree(t, dir, tree)

	defer func(n int) { maxRequestFiles = n }(maxRequestFiles)
	maxRequestFiles = 2
	before := len(fake.requests)
	report, log, err := syncFolder(t, c, dir, "", nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	got := fake.requests[before:]
	if len(got) != 3 || got[0] != "ask" || strings.Count(got[1], ",") != 1 || strings.Count(got[2], ",") != 1 {
		t.Errorf("requests = %v", got)
	}
	// "same" travelled once for both of its files.
	if n := strings.Count(strings.Join(got, " "), sha("same")[:8]); n != 1 {
		t.Errorf("the shared content was sent %d times: %v", n, got)
	}
	if report.Uploaded != 5 || report.Files != 6 {
		t.Errorf("report = %+v", report)
	}
	wantStored(t, fake, "src-1", tree)
}

func TestSyncFolderCreatesALargeFolderInSteps(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir, state := t.TempDir(), t.TempDir()
	tree := map[string]string{"a.md": "a", "b.md": "b", "c.md": "c", "d.md": "d", "e.md": "e"}
	writeTree(t, dir, tree)
	defer func(n int) { maxRequestFiles = n }(maxRequestFiles)
	maxRequestFiles = 2

	report, log, err := syncFolder(t, c, dir, state, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	want := "upload a.md,b.md ; ask ; send " + sha("c")[:8] + "," + sha("d")[:8] + " ; send " + sha("e")[:8]
	if fake.log() != want {
		t.Errorf("requests = %s\n    want %s", fake.log(), want)
	}
	if report.SourceID != "src-1" || !report.Synced || report.Status != "SUCCESS" || report.Files != 5 {
		t.Errorf("report = %+v", report)
	}
	wantStored(t, fake, "src-1", tree)
}

// The batch limits are for requests that can be split. An upload that cannot
// goes whole, as it always did: the server knows its own limits.
func TestUploadThatCannotBeSplitGoesInOneRequest(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir := t.TempDir()
	tree := map[string]string{"a.md": "a", "b.md": "b", "c.md": "c"}
	writeTree(t, dir, tree)
	defer func(n int) { maxRequestFiles = n }(maxRequestFiles)
	maxRequestFiles = 2

	plain := func(o *IngestOptions) { o.Sync = false }
	if _, log, err := syncFolder(t, c, dir, "", plain); err != nil {
		t.Fatalf("plain upload: %v\n%s", err, log)
	}
	whole := func(o *IngestOptions) { o.Whole = true }
	if _, log, err := syncFolder(t, c, dir, "", whole); err != nil {
		t.Fatalf("whole sync: %v\n%s", err, log)
	}
	if fake.log() != "upload a.md,b.md,c.md ; whole a.md,b.md,c.md" {
		t.Errorf("requests = %s", fake.log())
	}

	// The server refuses it: the error says how to send it in several.
	fake.maxFiles = 2
	_, _, err := syncFolder(t, c, dir, "", func(o *IngestOptions) { o.Name, o.Sync = "Plain", false })
	if err == nil || !strings.Contains(err.Error(), "--sync --wait") {
		t.Errorf("plain upload over the server's limit: %v", err)
	}
	_, _, err = syncFolder(t, c, dir, "", func(o *IngestOptions) { o.Name, o.Wait = "Later", false })
	if err == nil || !strings.Contains(err.Error(), "add --wait") {
		t.Errorf("sync without --wait over the server's limit: %v", err)
	}
	wantStored(t, fake, "src-1", tree)
}

// A sync's record belongs to the account that made it.
func TestSyncFolderRecordIsPerAccount(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir, state := t.TempDir(), t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "a"})
	if _, _, err := syncFolder(t, c, dir, state, nil); err != nil {
		t.Fatal(err)
	}
	other := New(c.BaseURL, "dgpt_pat_another_account", c.UserAgent)
	report, _, err := syncFolder(t, other, dir, state, nil)
	if err != nil || report.Local {
		t.Fatalf("another account must ask the server: %+v, %v", report, err)
	}
	if report, _, _ = syncFolder(t, c, dir, state, nil); !report.Local {
		t.Errorf("the first account keeps its record: %+v", report)
	}
}

// An Idempotency-Key the server knows replays the earlier sync's reply: no
// file is compared or sent, so nothing says the source holds this tree.
func TestSyncFolderReplayedByIdempotencyKey(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir, state := t.TempDir(), t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "a"})
	keyed := func(o *IngestOptions) { o.SyncKey = "nightly" }
	if _, _, err := syncFolder(t, c, dir, "", keyed); err != nil {
		t.Fatal(err)
	}
	writeTree(t, dir, map[string]string{"a.md": "a2"})

	fake.replay = true
	before := len(fake.requests)
	report, log, err := syncFolder(t, c, dir, state, keyed)
	if err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	if got := strings.Join(fake.requests[before:], " ; "); got != "replay" {
		t.Errorf("requests = %s, want one replayed request", got)
	}
	if !report.Deduplicated || report.TaskID != "task-old" || report.Uploaded != 0 || !strings.Contains(log, "deduplicated") {
		t.Errorf("report = %+v\n%s", report, log)
	}
	fake.replay = false
	if report, _, _ = syncFolder(t, c, dir, state, nil); report.Local {
		t.Errorf("a replayed sync must leave no record: %+v", report)
	}
	wantStored(t, fake, "src-1", map[string]string{"a.md": "a2"})
}

// A deduplicated first batch names no source for the other files.
func TestSyncFolderDeduplicatedFirstBatchIsNotTheWholeFolder(t *testing.T) {
	fake := newFakeSources(t)
	fake.dedupUpload = true
	c := newTestClient(t, fake.ServeHTTP)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "a", "b.md": "b", "c.md": "c"})
	defer func(n int) { maxRequestFiles = n }(maxRequestFiles)
	maxRequestFiles = 2

	report, _, err := syncFolder(t, c, dir, "", func(o *IngestOptions) { o.Key = "k" })
	if err == nil || !strings.Contains(err.Error(), "2 of 3 files") {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
}

func TestSyncFolderFallsBackOnAnOlderServer(t *testing.T) {
	fake := newFakeSources(t)
	fake.noManifests = true
	c := newTestClient(t, fake.ServeHTTP)
	dir, state := t.TempDir(), t.TempDir()
	tree := map[string]string{"a.md": "a", "文档/指南.md": "指南"}
	writeTree(t, dir, tree)
	if _, _, err := syncFolder(t, c, dir, state, nil); err != nil {
		t.Fatal(err)
	}
	tree["a.md"] = "a2"
	writeTree(t, dir, tree)

	report, log, err := syncFolder(t, c, dir, state, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	if !strings.HasSuffix(fake.log(), "whole a.md,文档/指南.md") || !strings.Contains(log, "only as a whole") {
		t.Errorf("requests = %s\n%s", fake.log(), log)
	}
	if report.TaskID != "task-whole" || report.Uploaded != 2 || !report.Synced {
		t.Errorf("report = %+v", report)
	}
	wantStored(t, fake, "src-1", tree)

	// Past the batch limits it still goes whole: this server has no other way.
	defer func(n int) { maxRequestFiles = n }(maxRequestFiles)
	maxRequestFiles = 1
	tree["b.md"] = "b"
	writeTree(t, dir, tree)
	if _, log, err = syncFolder(t, c, dir, state, nil); err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	wantStored(t, fake, "src-1", tree)
	// Unless the server refuses it.
	fake.maxFiles = 2
	_, _, err = syncFolder(t, c, dir, state, func(o *IngestOptions) { o.Force = true })
	if err == nil || !strings.Contains(err.Error(), "update the server") {
		t.Errorf("err = %v", err)
	}
}

func TestSyncWholeSendsEveryFile(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir, state := t.TempDir(), t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "a"})
	whole := func(o *IngestOptions) { o.Whole = true }
	if _, _, err := syncFolder(t, c, dir, state, whole); err != nil {
		t.Fatal(err)
	}

	report, _, err := syncFolder(t, c, dir, state, whole)
	if err != nil || report.Local || report.TaskID != "task-whole" {
		t.Fatalf("a whole sync keeps no record and sends everything: %+v, %v", report, err)
	}
	if !strings.HasSuffix(fake.log(), "whole a.md") {
		t.Errorf("requests = %s", fake.log())
	}
}

func TestSyncFolderRetriesABusySource(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "a"})
	if _, _, err := syncFolder(t, c, dir, "", nil); err != nil {
		t.Fatal(err)
	}
	writeTree(t, dir, map[string]string{"a.md": "a2"})

	fake.busy = 2
	report, log, err := syncFolder(t, c, dir, "", nil)
	if err != nil || report.Uploaded != 1 {
		t.Fatalf("%+v, %v\n%s", report, err, log)
	}
	if strings.Count(log, "trying again") != 2 {
		t.Errorf("log:\n%s", log)
	}

	fake.busy = 1000
	writeTree(t, dir, map[string]string{"a.md": "a3"})
	_, _, err = syncFolder(t, c, dir, "", func(o *IngestOptions) { o.Timeout = 20 * time.Millisecond })
	if err == nil || !strings.Contains(err.Error(), "busy with another change") {
		t.Errorf("err = %v", err)
	}
}

func TestSyncFolderReportsAFileChangedWhileUploading(t *testing.T) {
	fake := newFakeSources(t)
	c := newTestClient(t, fake.ServeHTTP)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "a"})
	if _, _, err := syncFolder(t, c, dir, "", nil); err != nil {
		t.Fatal(err)
	}
	writeTree(t, dir, map[string]string{"a.md": "edited"})
	found, _ := CollectFiles([]string{dir}, CollectOptions{})
	if err := HashFiles(found.Files); err != nil {
		t.Fatal(err)
	}
	// Same length, other bytes, after hashing.
	writeTree(t, dir, map[string]string{"a.md": "EDITED"})

	_, err := c.Ingest(context.Background(), IngestOptions{
		Name: "Docs", Files: found.Files, Sync: true, Log: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match its sha256") {
		t.Errorf("err = %v", err)
	}
}

func TestMultipartBodyCutsAFileThatGrew(t *testing.T) {
	dir := t.TempDir()
	p := writeTemp(t, dir, "a.md", "12345")
	body, _, length, err := multipartBody(nil, []filePart{{Field: "file", Filename: "a.md", Path: p}})
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	writeTemp(t, dir, "a.md", "1234567890")
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(raw)) != length || !bytes.Contains(raw, []byte("12345\r\n--")) {
		t.Errorf("body is %d bytes for a declared %d:\n%s", len(raw), length, raw)
	}
}

func TestNeedBatch(t *testing.T) {
	byName := map[string]UploadFile{
		"a": {Name: "a", SHA256: "h1", Size: 60},
		"b": {Name: "b", SHA256: "h2", Size: 60},
		"c": {Name: "c", SHA256: "h1", Size: 60},
		"d": {Name: "d", SHA256: "h3", Size: 500},
	}
	defer func(n int64) { maxRequestBytes = n }(maxRequestBytes)
	maxRequestBytes = 100

	batch, err := needBatch(byName, []string{"a", "c", "b", "d"})
	if err != nil || names(batch) != "a" {
		t.Errorf("batch = %s, %v", names(batch), err)
	}
	// A file over the limit still goes, alone.
	batch, err = needBatch(byName, []string{"d", "a"})
	if err != nil || names(batch) != "d" {
		t.Errorf("batch = %s, %v", names(batch), err)
	}
	if _, err := needBatch(byName, []string{"zzz"}); err == nil {
		t.Error("a name outside the sync must be refused")
	}
	if _, err := needBatch(byName, nil); err == nil {
		t.Error("needing nothing in particular must be refused")
	}
}
