package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"docsgpt-cli/internal/config"
	"docsgpt-cli/internal/display"
	"docsgpt-cli/internal/manage"

	"github.com/spf13/cobra"
)

const cmdTestToken = "dgpt_pat_AbCdEfSECRETSECRETSECRETSECRET"

func TestMain(m *testing.M) {
	display.InitTheme("auto")
	os.Exit(m.Run())
}

// isolateConfig points ~/.docsgpt at a temp dir and clears the token/URL
// flags and environment for the duration of the test.
func isolateConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvURL, "")
	oldToken, oldURL := globalToken, globalURL
	globalToken, globalURL = "", ""
	t.Cleanup(func() { globalToken, globalURL = oldToken, oldURL })
	return home
}

func jsonReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

const meBody = `{"success":true,"user_id":"user-1","roles":["user"],"auth_method":"pat",
	"token":{"id":"tok-1","name":"ci-deploy","scopes":["sources:write","agents:write","agents:read"],
	"resource_filter":{"agents":["agent-1","agent-2"]}}}`

func TestExitCodeFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"plain failure", errors.New("boom"), 1},
		{"usage", usageErrf("bad flag"), 2},
		{"wrapped usage", fmt.Errorf("context: %w", usageErrf("bad")), 2},
		{"explicit failure", &exitError{code: exitFailure, err: errors.New("blocked")}, 1},
		{"api error", &manage.APIError{Status: 403, Code: manage.CodeInsufficientScope}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCodeFor(tt.err); got != tt.want {
				t.Errorf("exitCodeFor() = %d, want %d", got, tt.want)
			}
		})
	}
	if usageErr(nil) != nil {
		t.Error("usageErr(nil) must stay nil")
	}
	if err := usageArgs(cobra.ExactArgs(1))(&cobra.Command{}, nil); exitCodeFor(err) != exitUsage {
		t.Errorf("arg validation should exit 2, got %v", err)
	}
}

func TestManagementCommandsSkipBanner(t *testing.T) {
	for _, c := range []*cobra.Command{loginCmd, logoutCmd, whoamiCmd, agentsApplyCmd, sourcesUploadCmd, promptsListCmd, toolsListCmd} {
		if !hasNoBanner(c) {
			t.Errorf("%s should skip the banner", c.CommandPath())
		}
		if !c.SilenceUsage {
			t.Errorf("%s should not dump usage on runtime errors", c.CommandPath())
		}
	}
	for _, c := range []*cobra.Command{askCmd, chatCmd, benchCmd} {
		if hasNoBanner(c) {
			t.Errorf("%s should keep the banner", c.CommandPath())
		}
	}
}

func TestLoginWhoamiLogout(t *testing.T) {
	home := isolateConfig(t)
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/user/me" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if gotAuth != "Bearer "+cmdTestToken {
			jsonReply(w, 401, `{"message":"Authentication error: invalid token","error":"invalid_token"}`)
			return
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "docsgpt-cli/") {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		jsonReply(w, 200, meBody)
	}))
	defer srv.Close()
	ctx := context.Background()
	cfgPath := filepath.Join(home, ".docsgpt", "config.json")

	// A rejected token is not stored.
	var out bytes.Buffer
	err := runLogin(ctx, "dgpt_pat_wrongwrongwrongwrong", srv.URL, &out)
	if err == nil || exitCodeFor(err) != exitFailure || !strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("bad token: err = %v", err)
	}
	if _, statErr := os.Stat(cfgPath); statErr == nil {
		t.Fatal("config written for a rejected token")
	}
	// So is something that is not a PAT at all (exit 2, no request made).
	gotAuth = ""
	if err := runLogin(ctx, "eyJhbGciOi.jwt.token", srv.URL, &out); exitCodeFor(err) != exitUsage || gotAuth != "" {
		t.Fatalf("non-PAT: err = %v, request made = %v", err, gotAuth != "")
	}

	out.Reset()
	if err := runLogin(ctx, cmdTestToken, srv.URL+"/", &out); err != nil {
		t.Fatalf("login: %v", err)
	}
	text := out.String()
	for _, want := range []string{"user-1", "ci-deploy", "agents:read, agents:write, sources:write", "agents: agent-1, agent-2", "dgpt_pat_AbCdEf…"} {
		if !strings.Contains(text, want) {
			t.Errorf("login output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "SECRET") {
		t.Errorf("login output leaks the token:\n%s", text)
	}
	cfg, err := config.Load()
	if err != nil || cfg.Token != cmdTestToken || cfg.BaseURL != srv.URL {
		t.Fatalf("stored config = %+v, err = %v", cfg, err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(cfgPath); st.Mode().Perm() != 0o600 {
			t.Errorf("config mode = %o, want 600", st.Mode().Perm())
		}
	}

	// whoami uses the stored token and base URL.
	out.Reset()
	if err := runWhoami(ctx, false, &out); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if !strings.Contains(out.String(), "from config file") || strings.Contains(out.String(), "SECRET") {
		t.Errorf("whoami output:\n%s", out.String())
	}
	out.Reset()
	if err := runWhoami(ctx, true, &out); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc["user_id"] != "user-1" {
		t.Errorf("whoami --json = %s (%v)", out.String(), err)
	}

	// The environment variable wins over the stored token.
	t.Setenv(config.EnvToken, "dgpt_pat_fromenvfromenvfromenv")
	if err := runWhoami(ctx, false, &out); !manage.IsUnauthorized(err) {
		t.Errorf("env token should have been sent and rejected, err = %v", err)
	}
	if gotAuth != "Bearer dgpt_pat_fromenvfromenvfromenv" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	// ... and the flag over both.
	globalToken = cmdTestToken
	if err := runWhoami(ctx, false, &out); err != nil {
		t.Errorf("flag token: %v", err)
	}
	globalToken = ""

	out.Reset()
	if err := runLogout(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "still set") {
		t.Errorf("logout should warn about %s:\n%s", config.EnvToken, out.String())
	}
	cfg, _ = config.Load()
	if cfg.Token != "" || cfg.BaseURL != srv.URL {
		t.Errorf("after logout: %+v", cfg)
	}
	t.Setenv(config.EnvToken, "")
	if err := runWhoami(ctx, false, &out); exitCodeFor(err) != exitUsage {
		t.Errorf("whoami without a token should be a usage error, got %v", err)
	}
	if _, err := newManageClient(); exitCodeFor(err) != exitUsage {
		t.Errorf("newManageClient without a token: %v", err)
	}
}

func TestReadLoginTokenFromPipe(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		piped   string
		want    string
		wantErr bool
	}{
		{"flag wins", " dgpt_pat_flag ", "dgpt_pat_pipe\n", "dgpt_pat_flag", false},
		{"piped, first line only", "", "dgpt_pat_pipe\nextra\n", "dgpt_pat_pipe", false},
		{"piped without newline", "", "dgpt_pat_pipe", "dgpt_pat_pipe", false},
		{"empty pipe", "", "\n", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			io.WriteString(w, tt.piped)
			w.Close()
			defer r.Close()
			got, err := readLoginToken(tt.flag, r, io.Discard)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("readLoginToken() = %q, %v; want %q, err %v", got, err, tt.want, tt.wantErr)
			}
			if tt.wantErr && exitCodeFor(err) != exitUsage {
				t.Errorf("exit code = %d, want 2", exitCodeFor(err))
			}
		})
	}
}

// importServer fakes the agent import API. planFor maps a document's
// spec.name to the plan the server returns.
type importServer struct {
	mu       sync.Mutex
	planFor  map[string]string
	planned  []string
	applied  []map[string]any
	failName string // apply of this agent returns 400
}

func (s *importServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		text, _ := body["yaml"].(string)
		name := ""
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "name: ") {
				name = strings.TrimPrefix(strings.TrimSpace(line), "name: ")
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/api/import_agent/plan":
			s.planned = append(s.planned, name)
			plan, ok := s.planFor[name]
			if !ok {
				plan = cleanPlan
			}
			jsonReply(w, 200, `{"success":true,"plan":`+plan+`}`)
		case "/api/import_agent":
			if name == s.failName {
				jsonReply(w, 400, `{"success":false,"message":"A published workflow agent needs a workflow; this file has none"}`)
				return
			}
			s.applied = append(s.applied, body)
			jsonReply(w, 200, `{"success":true,"agent_id":"id-`+name+`","action":"created","status":"draft","agent_type":"classic","slug":"`+strings.ToLower(name)+`","warnings":["Source 'X' not found; left unattached"]}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}
}

const cleanPlan = `{"target":{"action":"create","agent_id":null,"matched_by":null,"status":null},
	"sources":[{"name":"Docs","type":"file","status":"matched","target_id":"s1"}],
	"tools":[{"key":"tool-0","type":"brave","name":"search","status":"reuse","target_id":"t1"}],
	"prompt":{"status":"create","name":"support"},"models":[{"id":"gpt-x","status":"matched"}],"workflow":null}`

const blockedPlan = `{"target":{"action":"update","agent_id":"a-9","matched_by":"slug","status":"published"},
	"sources":[{"name":"Handbook","type":"file","status":"missing","target_id":null}],
	"tools":[{"key":"tool-0","type":"legacy","name":"old","status":"unavailable"}],
	"prompt":{"status":"default"},"models":[],"workflow":{"action":"update","nodes":3,"edges":2}}`

func agentYAML(name string) string {
	return "apiVersion: docsgpt.arc53.com/v1\nkind: Agent\nmetadata:\n  slug: " + strings.ToLower(name) + "\nspec:\n  name: " + name + "\n"
}

func TestAgentsApplyGating(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	clean := write("clean.yaml", agentYAML("Clean"))
	multi := write("multi.yaml", agentYAML("Clean")+"---\n"+agentYAML("Blocked"))
	blocked := write("blocked.yaml", agentYAML("Blocked"))
	failing := write("set/a-ok.yaml", agentYAML("First"))
	write("set/b-fails.yaml", agentYAML("Boom"))
	write("set/c-never.yaml", agentYAML("Never"))
	wrongKind := write("kind.yaml", agentYAML("Clean")+"---\napiVersion: docsgpt.arc53.com/v1\nkind: Source\nspec: {name: s}\n")

	tests := []struct {
		name        string
		opts        applyOptions
		wantExit    int
		wantPlanned int
		wantApplied int
		wantOut     []string
		wantErr     string
	}{
		{
			name: "clean document is applied", opts: applyOptions{Files: []string{clean}},
			wantPlanned: 1, wantApplied: 1,
			wantOut: []string{"CREATE", `"Docs"`, "matched", "tool tool-0", "reuse", "applied", "id-Clean", "warning:", "left unattached"},
		},
		{
			name: "dry run stops after the plan", opts: applyOptions{Files: []string{clean}, DryRun: true},
			wantPlanned: 1, wantApplied: 0, wantOut: []string{"dry run", "nothing applied"},
		},
		{
			name: "a blocked document blocks the whole batch", opts: applyOptions{Files: []string{multi}},
			wantExit: 1, wantPlanned: 2, wantApplied: 0,
			wantOut: []string{"UPDATE", "a-9", "matched by slug", "MISSING", "UNAVAILABLE", "blocked:", "3 nodes, 2 edges"},
			wantErr: "2 unresolved reference(s): nothing was applied",
		},
		{
			name: "blocked dry run also exits 1", opts: applyOptions{Files: []string{blocked}, DryRun: true},
			wantExit: 1, wantPlanned: 1, wantErr: "an apply would be refused",
		},
		{
			name: "partially resolved is still blocked", opts: applyOptions{Files: []string{blocked}, Resolve: []string{"source:Handbook=s-42"}},
			wantExit: 1, wantPlanned: 1, wantErr: "1 unresolved reference(s)",
		},
		{
			name:        "fully resolved is applied with the resolution",
			opts:        applyOptions{Files: []string{blocked}, Resolve: []string{"source:Handbook=s-42", "tool:old=skip"}},
			wantPlanned: 1, wantApplied: 1, wantOut: []string{"mapped to s-42", "skip"},
		},
		{
			name: "resolve matching nothing is a usage error", opts: applyOptions{Files: []string{clean}, Resolve: []string{"source:Nope=s1"}},
			wantExit: 2, wantPlanned: 1, wantErr: "does not match any reference",
		},
		{
			name: "positional tool key across documents", opts: applyOptions{Files: []string{multi}, Resolve: []string{"tool:tool-0=skip"}},
			wantExit: 2, wantErr: "ambiguous across 2 documents",
		},
		{
			name: "malformed resolve", opts: applyOptions{Files: []string{clean}, Resolve: []string{"nonsense"}},
			wantExit: 2, wantErr: "invalid --resolve",
		},
		{
			name: "non-Agent kind is rejected before any request", opts: applyOptions{Files: []string{wrongKind}},
			wantExit: 2, wantErr: `unsupported kind "Source"`,
		},
		{name: "no -f", opts: applyOptions{}, wantExit: 2, wantErr: "no input"},
		{
			name: "apply failure stops the batch", opts: applyOptions{Files: []string{filepath.Dir(failing)}},
			wantExit: 1, wantPlanned: 3, wantApplied: 1,
			wantErr: "needs a workflow; this file has none (1 earlier document(s) were already applied)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &importServer{planFor: map[string]string{"Blocked": blockedPlan}, failName: "Boom"}
			srv := httptest.NewServer(fake.handler(t))
			defer srv.Close()
			client := manage.New(srv.URL, cmdTestToken, "docsgpt-cli/test")

			var stdout, stderr bytes.Buffer
			err := runAgentsApply(context.Background(), client, tt.opts, strings.NewReader(""), &stdout, &stderr)
			if got := exitCodeFor(err); got != tt.wantExit {
				t.Fatalf("exit = %d (%v), want %d", got, err, tt.wantExit)
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
			if len(fake.planned) != tt.wantPlanned || len(fake.applied) != tt.wantApplied {
				t.Errorf("planned %d applied %d, want %d / %d", len(fake.planned), len(fake.applied), tt.wantPlanned, tt.wantApplied)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
				}
			}
			if tt.name == "fully resolved is applied with the resolution" {
				got, _ := json.Marshal(fake.applied[0]["resolution"])
				want := `{"sources":{"Handbook":"s-42"},"tools":{"tool-0":{"decision":"skip"}}}`
				if string(got) != want {
					t.Errorf("resolution = %s, want %s", got, want)
				}
			}
		})
	}
}

func TestAgentsApplyJSONAndStdin(t *testing.T) {
	fake := &importServer{planFor: map[string]string{"Blocked": blockedPlan}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	client := manage.New(srv.URL, cmdTestToken, "docsgpt-cli/test")

	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(agentYAML("Clean") + "---\n" + agentYAML("Blocked"))
	err := runAgentsApply(context.Background(), client, applyOptions{Files: []string{"-"}, JSON: true}, stdin, &stdout, &stderr)
	if exitCodeFor(err) != 1 {
		t.Fatalf("err = %v", err)
	}
	var report applyReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout.String())
	}
	if !report.Blocked || report.Applied != 0 || len(report.Documents) != 2 {
		t.Fatalf("report = %+v", report)
	}
	if report.Documents[0].Source != "<stdin>#1" || len(report.Documents[0].Blockers) != 0 ||
		len(report.Documents[1].Blockers) != 2 || report.Documents[1].Blockers[0].Kind != "source" {
		t.Errorf("documents = %+v", report.Documents)
	}
	if !strings.Contains(stderr.String(), "MISSING") {
		t.Errorf("the human plan should go to stderr with --json:\n%s", stderr.String())
	}
}

func TestSourcesUploadAndWait(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "guide.md")
	os.WriteFile(file, []byte("# guide"), 0o644)
	wantKey, _ := manage.DeriveIdempotencyKey("Guide", []string{file})
	fast := manage.WaitOptions{Initial: time.Millisecond, Max: 2 * time.Millisecond}

	tests := []struct {
		name      string
		opts      uploadOptions
		uploadRes string
		statuses  []string
		wantExit  int
		wantKey   string
		wantPolls bool
		wantOut   string
		wantErr   string
		wantLog   []string
	}{
		{
			name: "no wait", opts: uploadOptions{Files: []string{file}, Name: "Guide"},
			wantKey: wantKey, wantOut: "src-1", wantLog: []string{"ingestion queued"},
		},
		{
			name: "wait success", opts: uploadOptions{Files: []string{file}, Name: "Guide", Wait: true, Timeout: 5 * time.Second, Poll: fast},
			statuses: []string{`{"status":"PENDING"}`, `{"status":"PROGRESS","result":{"current":50}}`, `{"status":"SUCCESS","result":{}}`},
			wantKey:  wantKey, wantPolls: true, wantOut: "SUCCESS", wantLog: []string{"PENDING", "PROGRESS 50%", "SUCCESS"},
		},
		{
			name: "wait failure", opts: uploadOptions{Files: []string{file}, Name: "Guide", Wait: true, Timeout: 5 * time.Second, Poll: fast},
			statuses: []string{`{"status":"FAILURE","result":"no parser for .xyz"}`},
			wantKey:  wantKey, wantPolls: true, wantExit: 1, wantErr: "no parser for .xyz",
		},
		{
			name: "wait timeout", opts: uploadOptions{Files: []string{file}, Name: "Guide", Wait: true, Timeout: 30 * time.Millisecond, Poll: fast},
			statuses: []string{`{"status":"STARTED"}`},
			wantKey:  wantKey, wantPolls: true, wantExit: 1, wantErr: "timed out after 30ms",
		},
		{
			name: "explicit key", opts: uploadOptions{Files: []string{file}, Name: "Guide", Key: "build-42", KeySet: true},
			wantKey: "build-42",
		},
		{
			name: "explicit empty key sends no header", opts: uploadOptions{Files: []string{file}, Name: "Guide", Key: "", KeySet: true},
			wantKey: "",
		},
		{
			name: "deduplicated sentinel is not polled", opts: uploadOptions{Files: []string{file}, Name: "Guide", Wait: true, Timeout: time.Second, Poll: fast},
			uploadRes: `{"success":true,"task_id":"deduplicated"}`,
			wantKey:   wantKey, wantLog: []string{"deduplicated"},
		},
		{name: "missing name", opts: uploadOptions{Files: []string{file}}, wantExit: 2, wantErr: "--name is required"},
		{name: "missing file", opts: uploadOptions{Files: []string{filepath.Join(dir, "nope.md")}, Name: "Guide"}, wantExit: 2, wantErr: "no such file"},
		{name: "directory", opts: uploadOptions{Files: []string{dir}, Name: "Guide"}, wantExit: 2, wantErr: "is a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				mu      sync.Mutex
				gotKey  = "<no upload>"
				polls   int
				uploads int
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/api/upload":
					uploads++
					gotKey = r.Header.Get("Idempotency-Key")
					if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("name") != "Guide" || len(r.MultipartForm.File["file"]) != 1 {
						t.Errorf("bad multipart upload: %v", err)
					}
					res := tt.uploadRes
					if res == "" {
						res = `{"success":true,"task_id":"task-1","source_id":"src-1"}`
					}
					jsonReply(w, 200, res)
				case "/api/task_status":
					i := polls
					if i >= len(tt.statuses) {
						i = len(tt.statuses) - 1
					}
					polls++
					jsonReply(w, 200, tt.statuses[i])
				default:
					t.Errorf("unexpected %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			client := manage.New(srv.URL, cmdTestToken, "docsgpt-cli/test")

			var stdout, stderr bytes.Buffer
			err := runSourcesUpload(context.Background(), client, tt.opts, &stdout, &stderr)
			if got := exitCodeFor(err); got != tt.wantExit {
				t.Fatalf("exit = %d (%v), want %d", got, err, tt.wantExit)
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
			if tt.wantExit == 2 {
				if uploads != 0 {
					t.Errorf("a usage error must not upload anything")
				}
				return
			}
			if gotKey != tt.wantKey {
				t.Errorf("Idempotency-Key = %q, want %q", gotKey, tt.wantKey)
			}
			if (polls > 0) != tt.wantPolls {
				t.Errorf("polls = %d, wantPolls %v", polls, tt.wantPolls)
			}
			if tt.wantOut != "" && !strings.Contains(stdout.String(), tt.wantOut) {
				t.Errorf("stdout lacks %q: %s", tt.wantOut, stdout.String())
			}
			for _, want := range tt.wantLog {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
				}
			}
		})
	}
}

func TestSourcesUploadJSONReportsFailure(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.md")
	os.WriteFile(file, []byte("x"), 0o644)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/upload" {
			io.Copy(io.Discard, r.Body)
			jsonReply(w, 200, `{"success":true,"task_id":"task-1","source_id":"src-1"}`)
			return
		}
		jsonReply(w, 200, `{"status":"FAILURE","result":"boom"}`)
	}))
	defer srv.Close()
	var stdout, stderr bytes.Buffer
	err := runSourcesUpload(context.Background(), manage.New(srv.URL, cmdTestToken, ""), uploadOptions{
		Files: []string{file}, Name: "A", Wait: true, Timeout: time.Second, JSON: true,
		Poll: manage.WaitOptions{Initial: time.Millisecond},
	}, &stdout, &stderr)
	if exitCodeFor(err) != 1 {
		t.Fatalf("err = %v", err)
	}
	var report uploadReport
	if jsonErr := json.Unmarshal(stdout.Bytes(), &report); jsonErr != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", jsonErr, stdout.String())
	}
	if report.Status != "FAILURE" || report.SourceID != "src-1" || !report.Waited || !strings.Contains(report.Error, "boom") {
		t.Errorf("report = %+v", report)
	}
}

func TestListCommandsOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/get_agents":
			jsonReply(w, 200, `[{"id":"a1","name":"Support","slug":"support","agent_type":"classic","status":"published","ownership":"user"}]`)
		case "/api/sources":
			jsonReply(w, 200, `[{"id":"s1","name":"Docs","tokens":1234,"type":"file","date":"2026-01-01","ownership":"user"}]`)
		case "/api/get_prompts":
			jsonReply(w, 200, `[{"id":"default","name":"default","type":"public"}]`)
		case "/api/get_tools":
			jsonReply(w, 403, `{"error":"insufficient_scope","message":"Token lacks the required scope","required_scope":"tools:read"}`)
		}
	}))
	defer srv.Close()
	client := manage.New(srv.URL, cmdTestToken, "")
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(io.Writer, bool) error
		want []string
	}{
		{"agents", func(w io.Writer, j bool) error { return runAgentsList(ctx, client, j, w) }, []string{"a1", "Support", "published", "support"}},
		{"sources", func(w io.Writer, j bool) error { return runSourcesList(ctx, client, j, w) }, []string{"s1", "Docs", "1234"}},
		{"prompts", func(w io.Writer, j bool) error { return runPromptsList(ctx, client, j, w) }, []string{"default", "public"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var table, doc bytes.Buffer
			if err := tt.run(&table, false); err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(table.String(), want) {
					t.Errorf("table lacks %q:\n%s", want, table.String())
				}
			}
			if err := tt.run(&doc, true); err != nil {
				t.Fatal(err)
			}
			var arr []map[string]any
			if err := json.Unmarshal(doc.Bytes(), &arr); err != nil || len(arr) != 1 {
				t.Errorf("--json = %s (%v)", doc.String(), err)
			}
		})
	}

	err := runToolsList(ctx, client, false, io.Discard)
	if !manage.IsInsufficientScope(err) || !strings.Contains(err.Error(), `"tools:read"`) || exitCodeFor(err) != 1 {
		t.Errorf("tools list err = %v", err)
	}
}

func TestAgentsExportToFile(t *testing.T) {
	const doc = "apiVersion: docsgpt.arc53.com/v1\nkind: Agent\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
		io.WriteString(w, doc)
	}))
	defer srv.Close()
	client := manage.New(srv.URL, cmdTestToken, "")
	var stdout, stderr bytes.Buffer
	if err := runAgentsExport(context.Background(), client, "a1", "", &stdout, &stderr); err != nil || stdout.String() != doc {
		t.Fatalf("stdout export = %q, %v", stdout.String(), err)
	}
	out := filepath.Join(t.TempDir(), "agent.yaml")
	stdout.Reset()
	if err := runAgentsExport(context.Background(), client, "a1", out, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); string(data) != doc || stdout.Len() != 0 {
		t.Errorf("file = %q, stdout = %q", data, stdout.String())
	}
}

func TestConfirmDestructive(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		interactive bool
		wantExit    int
	}{
		{"yes", "y\n", true, 0},
		{"full yes", "YES\n", true, 0},
		{"default is no", "\n", true, 1},
		{"no", "n\n", true, 1},
		{"non-interactive refuses", "y\n", false, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := confirmDestructive(strings.NewReader(tt.input), io.Discard, tt.interactive, "Delete?")
			if got := exitCodeFor(err); got != tt.wantExit {
				t.Errorf("exit = %d (%v), want %d", got, err, tt.wantExit)
			}
		})
	}
}

// The token is stored with the server that accepted it even when that URL came
// from DOCSGPT_URL, so a later run without the variable cannot send it elsewhere.
func TestLoginStoresTheURLThatValidatedTheToken(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, meBody)
	}))
	defer srv.Close()
	t.Setenv(config.EnvURL, srv.URL+"/")

	var out bytes.Buffer
	if err := runLogin(context.Background(), cmdTestToken, "", &out); err != nil {
		t.Fatalf("login: %v", err)
	}
	t.Setenv(config.EnvURL, "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != srv.URL {
		t.Errorf("stored base_url = %q, want %q", cfg.BaseURL, srv.URL)
	}
	if got := cfg.ResolveURL(""); got != srv.URL {
		t.Errorf("ResolveURL without DOCSGPT_URL = %q, want %q", got, srv.URL)
	}
}

func TestSourcesUploadReplace(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "guide.md")
	os.WriteFile(file, []byte("# guide v2"), 0o644)
	fast := manage.WaitOptions{Initial: time.Millisecond, Max: 2 * time.Millisecond}
	const list = `[
		{"id":"src-new","name":"Guide","ownership":"user"},
		{"id":"src-old-1","name":"Guide","ownership":"user"},
		{"id":"src-old-2","name":"guide","ownership":"user"},
		{"id":"src-team","name":"Guide","ownership":"team"},
		{"id":"src-other","name":"Handbook","ownership":"user"}
	]`

	tests := []struct {
		name        string
		opts        uploadOptions
		taskStatus  string
		uploadRes   string
		wantExit    int
		wantErr     string
		wantDeleted []string
	}{
		{
			name: "deletes older same-named own sources after success", taskStatus: `{"status":"SUCCESS","result":{}}`,
			opts:        uploadOptions{Files: []string{file}, Name: "Guide", Wait: true, Replace: true, Timeout: 5 * time.Second, Poll: fast},
			wantDeleted: []string{"src-old-1", "src-old-2"},
		},
		{
			name: "deletes nothing when ingestion fails", taskStatus: `{"status":"FAILURE","result":"boom"}`,
			opts:     uploadOptions{Files: []string{file}, Name: "Guide", Wait: true, Replace: true, Timeout: 5 * time.Second, Poll: fast},
			wantExit: 1, wantErr: "boom",
		},
		{
			name: "deletes nothing without the new source id", taskStatus: `{"status":"SUCCESS","result":{}}`,
			uploadRes: `{"success":true,"task_id":"task-1"}`,
			opts:      uploadOptions{Files: []string{file}, Name: "Guide", Wait: true, Replace: true, Timeout: 5 * time.Second, Poll: fast},
		},
		{
			// Revert to earlier content: the repeated Idempotency-Key makes the
			// server answer with the source of that earlier upload, since deleted.
			name: "refuses when the reported source is not in the listing", taskStatus: `{"status":"SUCCESS","result":{}}`,
			uploadRes: `{"success":true,"task_id":"task-1","source_id":"src-gone"}`,
			opts:      uploadOptions{Files: []string{file}, Name: "Guide", Wait: true, Replace: true, Timeout: 5 * time.Second, Poll: fast},
			wantExit:  1, wantErr: "no longer exists",
		},
		{
			name: "deduplicated reply naming a deleted source deletes nothing", taskStatus: `{"status":"SUCCESS","result":{}}`,
			uploadRes: `{"success":true,"task_id":"deduplicated","source_id":"src-gone"}`,
			opts:      uploadOptions{Files: []string{file}, Name: "Guide", Wait: true, Replace: true, Timeout: 5 * time.Second, Poll: fast},
			wantExit:  1, wantErr: "nothing was deleted",
		},
		{
			name: "off by default", taskStatus: `{"status":"SUCCESS","result":{}}`,
			opts: uploadOptions{Files: []string{file}, Name: "Guide", Wait: true, Timeout: 5 * time.Second, Poll: fast},
		},
		{
			name: "needs --wait", opts: uploadOptions{Files: []string{file}, Name: "Guide", Replace: true},
			wantExit: 2, wantErr: "--replace needs --wait",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				mu      sync.Mutex
				deleted []string
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/api/upload":
					io.Copy(io.Discard, r.Body)
					res := tt.uploadRes
					if res == "" {
						res = `{"success":true,"task_id":"task-1","source_id":"src-new"}`
					}
					jsonReply(w, 200, res)
				case "/api/task_status":
					jsonReply(w, 200, tt.taskStatus)
				case "/api/sources":
					jsonReply(w, 200, list)
				case "/api/delete_old":
					deleted = append(deleted, r.URL.Query().Get("source_id"))
					jsonReply(w, 200, `{"success":true}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			var stdout, stderr bytes.Buffer
			err := runSourcesUpload(context.Background(), manage.New(srv.URL, cmdTestToken, ""), tt.opts, &stdout, &stderr)
			if got := exitCodeFor(err); got != tt.wantExit {
				t.Fatalf("exit = %d (%v), want %d", got, err, tt.wantExit)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
			if strings.Join(deleted, ",") != strings.Join(tt.wantDeleted, ",") {
				t.Errorf("deleted = %v, want %v", deleted, tt.wantDeleted)
			}
		})
	}
}
