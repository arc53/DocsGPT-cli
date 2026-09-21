package manage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUploadSourceMultipart(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.md", "alpha")
	b := writeTemp(t, dir, "b.txt", strings.Repeat("beta ", 5000))

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/upload" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.ContentLength <= 0 {
			t.Errorf("ContentLength = %d, want an explicit length (no chunked encoding)", r.ContentLength)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "key-1" {
			t.Errorf("Idempotency-Key = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if r.FormValue("name") != "Product docs" || r.FormValue("user") == "" {
			t.Errorf("form = %v", r.MultipartForm.Value)
		}
		files := r.MultipartForm.File["file"]
		if len(files) != 2 || files[0].Filename != "a.md" || files[1].Filename != "b.txt" {
			t.Fatalf("files = %+v", files)
		}
		f, _ := files[1].Open()
		body, _ := io.ReadAll(f)
		if string(body) != strings.Repeat("beta ", 5000) {
			t.Errorf("file b corrupted: %d bytes", len(body))
		}
		writeJSONResp(w, 200, `{"success":true,"task_id":"task-1","source_id":"src-1"}`)
	})
	res, err := c.UploadSource(context.Background(), "Product docs", []string{a, b}, "key-1")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if res.TaskID != "task-1" || res.SourceID != "src-1" {
		t.Errorf("result = %+v", res)
	}
}

func TestUploadSourceValidationAndErrors(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.md", "x")
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.Header.Get("Idempotency-Key") != "" {
			t.Errorf("no key expected, got %q", r.Header.Get("Idempotency-Key"))
		}
		writeJSONResp(w, 413, `{"success":false,"message":"File exceeds the upload limit"}`)
	})
	ctx := context.Background()
	tests := []struct {
		name  string
		src   string
		paths []string
		key   string
		want  string
	}{
		{"no name", "", []string{f}, "", "name is required"},
		{"no files", "n", nil, "", "at least one file"},
		{"missing file", "n", []string{filepath.Join(dir, "nope")}, "", "no such file"},
		{"directory", "n", []string{dir}, "", "is a directory"},
		{"long key", "n", []string{f}, strings.Repeat("k", 257), "exceeds 256"},
		{"server rejects", "n", []string{f}, "", "File exceeds the upload limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.UploadSource(ctx, tt.src, tt.paths, tt.key)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDeriveIdempotencyKey(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.md", "alpha")
	b := writeTemp(t, dir, "b.md", "beta")
	other := t.TempDir()
	aCopy := writeTemp(t, other, "a.md", "alpha")
	aChanged := writeTemp(t, t.TempDir(), "a.md", "alpha!")
	aRenamed := writeTemp(t, other, "renamed.md", "alpha")

	key := func(name string, paths ...string) string {
		t.Helper()
		k, err := DeriveIdempotencyKey(name, paths)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	base := key("docs", a, b)
	if !strings.HasPrefix(base, "docsgpt-cli-upload-") || len(base) > IdempotencyKeyMaxLen {
		t.Fatalf("key = %q", base)
	}
	tests := []struct {
		name string
		got  string
		same bool
	}{
		{"repeat", key("docs", a, b), true},
		{"file order", key("docs", b, a), true},
		{"same content from another directory", key("docs", aCopy, b), true},
		{"different name", key("docs2", a, b), false},
		{"changed content", key("docs", aChanged, b), false},
		{"renamed file", key("docs", aRenamed, b), false},
		{"fewer files", key("docs", a), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if (tt.got == base) != tt.same {
				t.Errorf("key equality = %v, want %v", tt.got == base, tt.same)
			}
		})
	}
	if _, err := DeriveIdempotencyKey("docs", []string{filepath.Join(dir, "missing")}); err == nil {
		t.Error("missing file should fail")
	}
}

// fastWait keeps polling tests quick while still exercising the backoff.
var fastWait = WaitOptions{Initial: time.Millisecond, Max: 4 * time.Millisecond, Factor: 2}

func TestWaitTask(t *testing.T) {
	tests := []struct {
		name       string
		replies    []string // "<status code> <body>", served in order; the last one repeats
		wantErr    string
		wantFailed bool
		wantSeen   []string
	}{
		{
			name: "pending then progress then success",
			replies: []string{
				`200 {"status":"PENDING","result":null}`,
				`503 {"success":false,"message":"Service unavailable"}`,
				`200 {"status":"PROGRESS","result":{"current":40}}`,
				`200 {"status":"SUCCESS","result":{"tokens":10}}`,
			},
			wantSeen: []string{"PENDING", "WAITING", "PROGRESS", "SUCCESS"},
		},
		{
			name:       "failure",
			replies:    []string{`200 {"status":"STARTED"}`, `200 {"status":"FAILURE","result":"parser exploded"}`},
			wantErr:    "parser exploded",
			wantFailed: true,
			wantSeen:   []string{"STARTED", "FAILURE"},
		},
		{
			name:    "hard http error stops polling",
			replies: []string{`403 {"error":"insufficient_scope","required_scope":"sources:read","message":"no"}`},
			wantErr: "sources:read",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/task_status" || r.URL.Query().Get("task_id") != "task-1" {
					t.Errorf("unexpected %s", r.URL.String())
				}
				i := int(calls.Add(1)) - 1
				if i >= len(tt.replies) {
					i = len(tt.replies) - 1
				}
				var status int
				var body string
				fmt.Sscanf(tt.replies[i], "%d", &status)
				body = tt.replies[i][4:]
				writeJSONResp(w, status, body)
			})
			var seen []string
			opts := fastWait
			opts.OnUpdate = func(status string, _ *TaskStatus) { seen = append(seen, status) }
			st, err := c.WaitTask(context.Background(), "task-1", opts)
			if tt.wantErr == "" {
				if err != nil || st == nil || st.Status != "SUCCESS" {
					t.Fatalf("st = %+v, err = %v", st, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			var tf *TaskFailedError
			if errors.As(err, &tf) != tt.wantFailed {
				t.Errorf("TaskFailedError = %v, want %v (%v)", !tt.wantFailed, tt.wantFailed, err)
			}
			if tt.wantSeen != nil && strings.Join(seen, ",") != strings.Join(tt.wantSeen, ",") {
				t.Errorf("updates = %v, want %v", seen, tt.wantSeen)
			}
		})
	}
}

func TestWaitTaskTimeout(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSONResp(w, 200, `{"status":"STARTED"}`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := c.WaitTask(ctx, "task-1", fastWait)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "last status: STARTED") {
		t.Errorf("error should name the last status: %v", err)
	}
}

func TestTaskStatusProgress(t *testing.T) {
	tests := []struct {
		result string
		want   int
		ok     bool
	}{
		{`{"current": 40}`, 40, true},
		{`{"current": 99.6}`, 99, true},
		{`{"other": 1}`, 0, false},
		{`"text"`, 0, false},
		{``, 0, false},
	}
	for _, tt := range tests {
		st := &TaskStatus{Result: []byte(tt.result)}
		got, ok := st.Progress()
		if got != tt.want || ok != tt.ok {
			t.Errorf("Progress(%s) = %d, %v; want %d, %v", tt.result, got, ok, tt.want, tt.ok)
		}
	}
}
