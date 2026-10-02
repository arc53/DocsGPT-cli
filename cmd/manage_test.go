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

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/manage"

	"github.com/spf13/cobra"
)

const cmdTestToken = "dgpt_pat_AbCdEfSECRETSECRETSECRETSECRET"

func TestMain(m *testing.M) {
	display.InitTheme("auto")
	questionArgs()
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
	t.Setenv(config.EnvAPIKey, "")
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

// runRoot executes the root command with args, as the binary would.
func runRoot(t *testing.T, args ...string) error {
	t.Helper()
	rootCmd.SetArgs(args)
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	t.Cleanup(func() { rootCmd.SetArgs(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil) })
	return rootCmd.Execute()
}

func TestRootUsageErrors(t *testing.T) {
	isolateConfig(t)
	if !rootCmd.SilenceUsage {
		t.Error("runtime errors should not dump usage")
	}
	for _, args := range [][]string{
		{"agents", "list", "--bogus"},
		{"--bogus", "question"},
		{"agnets"},
		{"agnets", "list"},
		{"agents", "lst"},
		{"config", "sett"},
	} {
		if err := runRoot(t, args...); exitCodeFor(err) != exitUsage {
			t.Errorf("%q: err = %v, want a usage error", args, err)
		}
	}
	// A typo hint names the command and how to ask anyway.
	err := runRoot(t, "agnets")
	if err == nil || !strings.Contains(err.Error(), `"agents"`) || !strings.Contains(err.Error(), `docsgpt-cli -- "agnets"`) {
		t.Errorf("typo hint = %v", err)
	}
}

// TestQuestionArgs: words after a command that takes none are a usage
// error pointing at how to ask them instead, and nothing runs.
func TestQuestionArgs(t *testing.T) {
	isolateConfig(t)
	for _, args := range [][]string{
		{"update", "my", "nginx", "config"},
		{"install", "my", "thing"},
		{"host", "my", "files"},
		{"bench", "press", "form", "tips"},
		{"login", "to", "my", "server"},
		{"config", "my", "nginx"},
	} {
		err := runRoot(t, args...)
		if hint := `docsgpt-cli -- "` + strings.Join(args, " ") + `"`; exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), hint) {
			t.Errorf("%q: err = %v, want a usage error with %s", args, err, hint)
		}
	}
}

func TestCommandTypo(t *testing.T) {
	for _, tt := range []struct {
		args []string
		typo bool
	}{
		{[]string{"agnets"}, true},
		{[]string{"Agents"}, true},
		{[]string{"agent", "list"}, true},
		{[]string{"how", "do", "I", "list", "files?"}, false},
		{[]string{"how do I rotate the key?"}, false},
		{[]string{"kubernetes"}, false},
		{[]string{"list", "agents"}, false},
	} {
		if got := commandTypo(rootCmd, tt.args) != nil; got != tt.typo {
			t.Errorf("commandTypo(%q) = %v, want %v", tt.args, got, tt.typo)
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

	// Without a terminal, logout needs --yes.
	logoutToken = tokenSwitch{on: true}
	t.Cleanup(func() { logoutToken, logoutYes = tokenSwitch{}, false })
	if err := runLogout(nil, &out); exitCodeFor(err) != exitUsage {
		t.Fatalf("logout without --yes: %v", err)
	}
	logoutYes = true
	out.Reset()
	if err := runLogout(nil, &out); err != nil {
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
	if err := runWhoami(ctx, false, &out); exitCodeFor(err) != exitFailure || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("whoami when logged out should exit 1, got %v", err)
	}
	if err := runWhoami(ctx, true, &out); exitCodeFor(err) != exitFailure {
		t.Errorf("whoami --json without a token should exit 1, got %v", err)
	}
	if _, err := newManageClient(); exitCodeFor(err) != exitUsage {
		t.Errorf("newManageClient without a token: %v", err)
	}
}

// TestLoginTokenKeepsKeysOnTheirServer: logging in to another server off a
// terminal must not silently move the stored agent keys there.
func TestLoginTokenKeepsKeysOnTheirServer(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, meBody)
	}))
	defer srv.Close()
	cfg := config.DefaultConfig()
	cfg.BaseURL = "https://docs.example.com"
	cfg.Keys["support"] = "0123abcd-0000-1111-2222-333344445555"
	cfg.DefaultKey = "support"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runLogin(context.Background(), cmdTestToken, srv.URL, &out)
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "support") {
		t.Fatalf("login to another server: err = %v, want a usage error naming the keys", err)
	}
	if cfg, _ := config.Load(); cfg.BaseURL != "https://docs.example.com" || cfg.Token != "" {
		t.Errorf("config changed: base %q, token stored %v", cfg.BaseURL, cfg.Token != "")
	}

	cfg.BaseURL = srv.URL + "/" // the same server: fine
	cfg.Save()
	if err := runLogin(context.Background(), cmdTestToken, srv.URL, &out); err != nil {
		t.Fatalf("login to the keys' server: %v", err)
	}
}

func TestLoginAgentKey(t *testing.T) {
	isolateConfig(t)
	const goodKey = "0123abcd-0000-1111-2222-333344445555"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+goodKey {
			jsonReply(w, 401, `{"error":{"message":"Invalid API key","type":"auth_error"}}`)
			return
		}
		jsonReply(w, 200, `{"object":"list","data":[{"id":"a-1","name":"Support Bot","object":"model"}]}`)
	}))
	defer srv.Close()
	globalURL = srv.URL
	ctx := context.Background()
	var out bytes.Buffer

	if err := loginKey(ctx, "wrong-key-wrong-key", "", &out); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("bad key: %v", err)
	}
	// Named after the agent, and the default.
	if err := loginKey(ctx, goodKey, "", &out); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	if cfg.Keys["support-bot"] != goodKey || cfg.DefaultKey != "support-bot" || cfg.BaseURL != srv.URL {
		t.Fatalf("stored config = %+v", cfg)
	}
	// The same key again keeps its name; another name for it is explicit.
	if err := loginKey(ctx, goodKey, "", &out); err != nil {
		t.Fatal(err)
	}
	if err := loginKey(ctx, goodKey, "ci", &out); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load()
	if len(cfg.Keys) != 2 || cfg.DefaultKey != "ci" {
		t.Fatalf("keys = %v, default %q", cfg.Keys, cfg.DefaultKey)
	}
	if strings.Contains(out.String(), goodKey) {
		t.Errorf("output leaks the key:\n%s", out.String())
	}

	out.Reset()
	if err := runWhoami(ctx, false, &out); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	for _, want := range []string{"ci (default)", "0123…5555", "Support Bot", "Personal access token: none"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("whoami lacks %q:\n%s", want, out.String())
		}
	}

	// Removing the default promotes the next key.
	logoutYes = true
	t.Cleanup(func() { logoutYes = false })
	if err := runLogout([]string{"ci"}, &out); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load()
	if len(cfg.Keys) != 1 || cfg.DefaultKey != "support-bot" {
		t.Fatalf("after logout: keys = %v, default %q", cfg.Keys, cfg.DefaultKey)
	}
	if err := runLogout([]string{"nope"}, &out); exitCodeFor(err) != exitUsage {
		t.Errorf("unknown key: %v", err)
	}
}

// logout --token shadows the global --token <pat>: a token given to it, or
// alone, names the stored token and is never looked up as a key name.
func TestLogoutByToken(t *testing.T) {
	isolateConfig(t)
	cfg := config.DefaultConfig()
	cfg.Keys["support"] = "k-1"
	cfg.Token = cmdTestToken
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	logoutYes = true
	t.Cleanup(func() { logoutToken, logoutYes = tokenSwitch{}, false })

	var out bytes.Buffer
	other := "dgpt_pat_OtherOtherOTHERSECRET"
	err := runLogout([]string{other}, &out)
	if exitCodeFor(err) != exitUsage || strings.Contains(err.Error(), "OTHERSECRET") || !strings.Contains(err.Error(), "not the stored access token") {
		t.Fatalf("other token: %v", err)
	}

	var sw tokenSwitch
	if err := sw.Set(cmdTestToken); err != nil || !sw.on || sw.value != cmdTestToken {
		t.Fatalf("--token=<pat>: %+v %v", sw, err)
	}
	if err := sw.Set("maybe"); err == nil {
		t.Error("--token=maybe should not parse")
	}
	logoutToken = tokenSwitch{on: true}
	if err := runLogout([]string{cmdTestToken}, &out); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = config.Load(); cfg.Token != "" || cfg.Keys["support"] != "k-1" {
		t.Errorf("after logout --token <pat>: token %q, keys %v", cfg.Token, cfg.Keys)
	}
}

func TestChatKeyWithoutKeyOffTerminal(t *testing.T) {
	isolateConfig(t)
	cfg := config.DefaultConfig()
	_, _, err := chatKey(&cfg)
	if err == nil || !strings.Contains(err.Error(), "docsgpt-cli login") || !strings.Contains(err.Error(), config.EnvAPIKey) || exitCodeFor(err) != exitFailure {
		t.Fatalf("chatKey() = %v", err)
	}
	t.Setenv(config.EnvAPIKey, "k-env")
	if name, key, err := chatKey(&cfg); err != nil || key != "k-env" || name != config.EnvAPIKey {
		t.Errorf("chatKey() with %s = %q %q %v", config.EnvAPIKey, name, key, err)
	}
}

func TestReadPipedSecret(t *testing.T) {
	tests := []struct {
		name    string
		piped   string
		want    string
		wantErr bool
	}{
		{"first line only", "dgpt_pat_pipe\nextra\n", "dgpt_pat_pipe", false},
		{"without newline", " key-1 ", "key-1", false},
		{"empty pipe", "\n", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readPipedSecret(strings.NewReader(tt.piped))
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("readPipedSecret() = %q, %v; want %q, err %v", got, err, tt.want, tt.wantErr)
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
			// A timed-out poll may still be in the handler.
			srv.Close()
			mu.Lock()
			defer mu.Unlock()
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
	var report manage.UploadReport
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
			jsonReply(w, 200, `[{"id":"a1","name":"Sup\u001b[2Jport","slug":"support","agent_type":"classic","status":"published","ownership":"user"}]`)
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
		{"agents", func(w io.Writer, j bool) error { return runAgentsList(ctx, client, j, w) }, []string{"a1", "Sup␛[2Jport", "published", "support"}},
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
			if strings.Contains(table.String(), "\x1b") {
				t.Errorf("table carries a raw escape:\n%q", table.String())
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

// Off a terminal (as under go test), a destructive action needs --yes.
func TestConfirmDestructive(t *testing.T) {
	if err := confirmDestructive("Delete?"); exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("err = %v, want a usage error asking for --yes", err)
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

const hookTestToken = "hookSECRETtok_abc-123"

// triggerServer fakes the agent webhook, /api/agent_webhook and task_status.
type triggerServer struct {
	t        *testing.T
	statuses []string // task_status replies ("<code> <body>"), in order; the last repeats
	hookResp string   // webhook reply; default a task_id
	// statusAuth makes task_status answer 401 unless it carries the PAT.
	statusAuth bool

	mu          sync.Mutex
	posts       int
	postAuth    string
	postUA      string
	postKey     string
	postBody    string
	lookups     int
	lookupAuth  string
	polls       int
	pollAuth    []string
	lookupAgent string
}

func (s *triggerServer) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.URL.Path == manage.WebhookPath+hookTestToken && r.Method == http.MethodPost:
		s.posts++
		s.postAuth, s.postUA, s.postKey = r.Header.Get("Authorization"), r.Header.Get("User-Agent"), r.Header.Get("Idempotency-Key")
		b, _ := io.ReadAll(r.Body)
		s.postBody = string(b)
		if r.Header.Get("Content-Type") != "application/json" {
			s.t.Errorf("webhook Content-Type = %q", r.Header.Get("Content-Type"))
		}
		res := s.hookResp
		if res == "" {
			res = `200 {"success":true,"task_id":"task-1"}`
		}
		var code int
		fmt.Sscanf(res, "%d", &code)
		jsonReply(w, code, res[4:])
	case r.URL.Path == "/api/agent_webhook":
		s.lookups++
		s.lookupAuth, s.lookupAgent = r.Header.Get("Authorization"), r.URL.Query().Get("id")
		// API_URL on the server is not the address the CLI uses.
		jsonReply(w, 200, `{"success":true,"webhook_url":"http://localhost:7091/api/webhooks/agents/`+hookTestToken+`"}`)
	case r.URL.Path == "/api/task_status":
		s.polls++
		auth := r.Header.Get("Authorization")
		s.pollAuth = append(s.pollAuth, auth)
		if s.statusAuth && auth != "Bearer "+cmdTestToken {
			jsonReply(w, 401, `{"message":"Authentication required","error":"unauthorized"}`)
			return
		}
		i := s.polls - 1
		if i >= len(s.statuses) {
			i = len(s.statuses) - 1
		}
		var code int
		fmt.Sscanf(s.statuses[i], "%d", &code)
		jsonReply(w, code, s.statuses[i][4:])
	default:
		s.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		jsonReply(w, 404, `{"success":false}`)
	}
}

func writePayload(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const triggerPayload = `{"kind":"diagnostic","repo":"arc53/DocsGPT","trigger":{"manual":true}}`

func TestAgentsTrigger(t *testing.T) {
	payload := writePayload(t, triggerPayload+"\n")
	fast := manage.WaitOptions{Initial: time.Millisecond, Max: 2 * time.Millisecond}
	success := `200 {"status":"SUCCESS","result":{"status":"success","result":{"answer":"login: arc53-machine\n","sources":[],"tool_calls":[{"tool_name":"get_me"}],"thought":""}}}`

	tests := []struct {
		name       string
		opts       triggerOptions // WebhookURL "HOOK" is replaced by the server's webhook URL, BaseURL "" by the server URL
		stdin      string
		statuses   []string
		hookResp   string
		statusAuth bool
		wantExit   int
		wantOut    string
		wantErr    string
		wantLog    []string
		wantPosts  int
		wantKey    string
		wantLookup bool
	}{
		{
			name:    "webhook url, file, no wait prints the task id",
			opts:    triggerOptions{WebhookURL: "HOOK", File: payload, Key: "pr-12-abc"},
			wantOut: "task-1\n", wantLog: []string{"api/webhooks/agents/...", "queued as task task-1"},
			wantPosts: 1, wantKey: "pr-12-abc",
		},
		{
			name: "stdin payload", opts: triggerOptions{WebhookURL: "HOOK", File: "-"}, stdin: "  " + triggerPayload + "\n",
			wantOut: "task-1\n", wantPosts: 1,
		},
		{
			name:    "agent id resolves the webhook with the token",
			opts:    triggerOptions{AgentID: "agent-1", File: payload, Token: cmdTestToken},
			wantOut: "task-1\n", wantPosts: 1, wantLookup: true,
		},
		{
			name: "wait success prints the answer",
			opts: triggerOptions{WebhookURL: "HOOK", File: payload, Wait: true, Timeout: 5 * time.Second, Poll: fast},
			statuses: []string{`200 {"status":"PENDING","result":null}`, `503 {"success":false,"message":"Service unavailable"}`,
				`200 {"status":"PROGRESS","result":{"current":50}}`, success},
			wantOut: "login: arc53-machine\n", wantPosts: 1,
			wantLog: []string{"PENDING", "WAITING", "PROGRESS", "SUCCESS", "1 tool call(s)"},
		},
		{
			name:     "wait failure",
			opts:     triggerOptions{WebhookURL: "HOOK", File: payload, Wait: true, Timeout: 5 * time.Second, Poll: fast},
			statuses: []string{`200 {"status":"STARTED"}`, `200 {"status":"FAILURE","result":"LLM provider error"}`},
			wantExit: 1, wantErr: "LLM provider error", wantPosts: 1,
		},
		{
			name:     "agent-level failure inside a SUCCESS task",
			opts:     triggerOptions{WebhookURL: "HOOK", File: payload, Wait: true, Timeout: 5 * time.Second, Poll: fast},
			statuses: []string{`200 {"status":"SUCCESS","result":{"status":"quota_exceeded","error":"Monthly token quota reached"}}`},
			wantExit: 1, wantErr: "quota_exceeded", wantPosts: 1,
		},
		{
			name:     "wait timeout",
			opts:     triggerOptions{WebhookURL: "HOOK", File: payload, Wait: true, Timeout: 30 * time.Millisecond, Poll: fast},
			statuses: []string{`200 {"status":"STARTED"}`},
			wantExit: 1, wantErr: "timed out after 30ms", wantPosts: 1,
		},
		{
			name:     "deduplicated sentinel is not polled",
			opts:     triggerOptions{WebhookURL: "HOOK", File: payload, Key: "k1", Wait: true, Timeout: time.Second, Poll: fast},
			hookResp: `200 {"success":true,"task_id":"deduplicated"}`,
			wantLog:  []string{"deduplicated"}, wantPosts: 1, wantKey: "k1",
		},
		{
			name: "webhook error", opts: triggerOptions{WebhookURL: "HOOK", File: payload},
			hookResp: `404 {"success":false,"message":"Agent not found"}`,
			wantExit: 1, wantErr: "Agent not found", wantPosts: 1,
		},
		{
			name: "server echoing the token is redacted", opts: triggerOptions{WebhookURL: "HOOK", File: payload},
			hookResp: `400 {"success":false,"message":"bad webhook ` + hookTestToken + `"}`,
			wantExit: 1, wantErr: "bad webhook ...", wantPosts: 1,
		},
		{
			name:       "task_status needing auth without a token",
			opts:       triggerOptions{WebhookURL: "HOOK", File: payload, Wait: true, Timeout: time.Second, Poll: fast},
			statusAuth: true, statuses: []string{success},
			wantExit: 2, wantErr: "requires authentication", wantPosts: 1,
		},
		{
			name:       "task_status needing auth falls back to the token on the same host",
			opts:       triggerOptions{WebhookURL: "HOOK", File: payload, Token: cmdTestToken, Wait: true, Timeout: time.Second, Poll: fast},
			statusAuth: true, statuses: []string{success},
			wantOut: "login: arc53-machine\n", wantPosts: 1, wantLog: []string{"polling with the personal access token"},
		},
		{
			name: "the token is never sent to another host",
			opts: triggerOptions{WebhookURL: "HOOK", File: payload, Token: cmdTestToken, BaseURL: "https://other.example",
				Wait: true, Timeout: time.Second, Poll: fast},
			statusAuth: true, statuses: []string{success},
			wantExit: 2, wantErr: "only sent to https://other.example", wantPosts: 1,
		},
		{name: "both targets", opts: triggerOptions{AgentID: "agent-1", WebhookURL: "HOOK", File: payload}, wantExit: 2, wantErr: "not both"},
		{name: "no target", opts: triggerOptions{File: payload}, wantExit: 2, wantErr: "DOCSGPT_WEBHOOK_URL"},
		{name: "no payload", opts: triggerOptions{WebhookURL: "HOOK"}, wantExit: 2, wantErr: "-f <file>"},
		{name: "agent id without a token", opts: triggerOptions{AgentID: "agent-1", File: payload}, wantExit: 2, wantErr: "agents:keys"},
		{name: "invalid JSON", opts: triggerOptions{WebhookURL: "HOOK", File: writePayload(t, `{"kind":`)}, wantExit: 2, wantErr: "not valid JSON"},
		{name: "invalid JSON on stdin", opts: triggerOptions{WebhookURL: "HOOK", File: "-"}, stdin: "kind: yaml", wantExit: 2, wantErr: "payload from stdin is not valid JSON"},
		{name: "empty payload", opts: triggerOptions{WebhookURL: "HOOK", File: writePayload(t, " \n")}, wantExit: 2, wantErr: "empty"},
		{name: "null payload", opts: triggerOptions{WebhookURL: "HOOK", File: writePayload(t, "null")}, wantExit: 2, wantErr: "JSON null"},
		{name: "missing file", opts: triggerOptions{WebhookURL: "HOOK", File: "/nonexistent/payload.json"}, wantExit: 2, wantErr: "no such file"},
		{name: "malformed webhook url", opts: triggerOptions{WebhookURL: "https://example.com/api/answer", File: payload}, wantExit: 2, wantErr: "--webhook-url"},
		{
			name: "idempotency key too long", opts: triggerOptions{WebhookURL: "HOOK", File: payload, Key: strings.Repeat("k", 257)},
			wantExit: 2, wantErr: "exceeds 256",
		},
		{name: "non-positive timeout", opts: triggerOptions{WebhookURL: "HOOK", File: payload, Wait: true}, wantExit: 2, wantErr: "--timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := &triggerServer{t: t, statuses: tt.statuses, hookResp: tt.hookResp, statusAuth: tt.statusAuth}
			srv := httptest.NewServer(http.HandlerFunc(ts.handler))
			defer srv.Close()
			opts := tt.opts
			if opts.WebhookURL == "HOOK" {
				opts.WebhookURL = srv.URL + manage.WebhookPath + hookTestToken
			}
			if opts.BaseURL == "" {
				opts.BaseURL = srv.URL
			}
			opts.UserAgent = "docsgpt-cli/test"

			var stdout, stderr bytes.Buffer
			err := runAgentsTrigger(context.Background(), opts, strings.NewReader(tt.stdin), &stdout, &stderr)
			if got := exitCodeFor(err); got != tt.wantExit {
				t.Fatalf("exit = %d, want %d (err %v)\nstderr: %s", got, tt.wantExit, err, stderr.String())
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
			if tt.wantOut != "" && stdout.String() != tt.wantOut {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantOut)
			}
			for _, want := range tt.wantLog {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
				}
			}
			all := stdout.String() + stderr.String()
			if err != nil {
				all += err.Error()
			}
			if strings.Contains(all, hookTestToken) {
				t.Errorf("the webhook token leaked:\n%s", all)
			}

			ts.mu.Lock()
			defer ts.mu.Unlock()
			if ts.posts != tt.wantPosts {
				t.Errorf("webhook posts = %d, want %d", ts.posts, tt.wantPosts)
			}
			if ts.posts > 0 {
				if ts.postAuth != "" {
					t.Errorf("the webhook got an Authorization header")
				}
				if ts.postUA != "docsgpt-cli/test" || ts.postKey != tt.wantKey {
					t.Errorf("webhook UA %q, Idempotency-Key %q (want %q)", ts.postUA, ts.postKey, tt.wantKey)
				}
				if ts.postBody != triggerPayload {
					t.Errorf("webhook body = %q", ts.postBody)
				}
			}
			if tt.wantLookup != (ts.lookups > 0) {
				t.Errorf("agent_webhook lookups = %d", ts.lookups)
			}
			if tt.wantLookup && (ts.lookupAuth != "Bearer "+cmdTestToken || ts.lookupAgent != "agent-1") {
				t.Errorf("lookup auth %q agent %q", ts.lookupAuth, ts.lookupAgent)
			}
			if len(ts.pollAuth) > 0 && ts.pollAuth[0] != "" {
				t.Errorf("the first task_status poll must be anonymous, got %q", ts.pollAuth[0])
			}
			if opts.BaseURL != srv.URL {
				for _, a := range ts.pollAuth {
					if a != "" {
						t.Errorf("the token was sent to a host it was not configured for")
					}
				}
			}
		})
	}
}

func TestAgentsTriggerJSON(t *testing.T) {
	payload := writePayload(t, triggerPayload)
	fast := manage.WaitOptions{Initial: time.Millisecond}
	ts := &triggerServer{t: t, statuses: []string{
		`200 {"status":"SUCCESS","result":{"status":"success","result":{"answer":"hi","tool_calls":[]}}}`,
	}}
	srv := httptest.NewServer(http.HandlerFunc(ts.handler))
	defer srv.Close()
	hook := srv.URL + manage.WebhookPath + hookTestToken

	var stdout, stderr bytes.Buffer
	err := runAgentsTrigger(context.Background(), triggerOptions{
		WebhookURL: hook, File: payload, Key: "k", Wait: true, Timeout: time.Second, JSON: true, Poll: fast, BaseURL: srv.URL,
	}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var report manage.TriggerReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if report.TaskID != "task-1" || report.Status != "SUCCESS" || report.Answer != "hi" || !report.Waited ||
		report.IdempotencyKey != "k" || !strings.Contains(string(report.Result), `"answer": "hi"`) {
		t.Errorf("report = %+v", report)
	}

	// A failed run is still a JSON document, with the error and exit 1.
	ts.statuses = []string{`200 {"status":"FAILURE","result":"boom"}`}
	ts.polls = 0
	stdout.Reset()
	err = runAgentsTrigger(context.Background(), triggerOptions{
		WebhookURL: hook, File: payload, Wait: true, Timeout: time.Second, JSON: true, Poll: fast, BaseURL: srv.URL,
	}, nil, &stdout, &stderr)
	if exitCodeFor(err) != 1 {
		t.Fatalf("err = %v", err)
	}
	report = manage.TriggerReport{}
	if jsonErr := json.Unmarshal(stdout.Bytes(), &report); jsonErr != nil || report.Status != "FAILURE" || !strings.Contains(report.Error, "boom") {
		t.Errorf("report = %+v (%v)", report, jsonErr)
	}

	// Without --wait the document carries the task id only.
	stdout.Reset()
	if err := runAgentsTrigger(context.Background(), triggerOptions{WebhookURL: hook, File: payload, JSON: true, BaseURL: srv.URL}, nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"task_id": "task-1"`) || !strings.Contains(stdout.String(), `"waited": false`) {
		t.Errorf("stdout = %s", stdout.String())
	}
}

// The command reads the webhook URL from DOCSGPT_WEBHOOK_URL only when
// neither --webhook-url nor an agent id is given.
func TestAgentsTriggerWebhookURLFromEnv(t *testing.T) {
	isolateConfig(t)
	ts := &triggerServer{t: t}
	srv := httptest.NewServer(http.HandlerFunc(ts.handler))
	defer srv.Close()
	t.Setenv(config.EnvWebhookURL, srv.URL+manage.WebhookPath+hookTestToken)
	old := agentsTriggerFile
	agentsTriggerFile = writePayload(t, triggerPayload)
	t.Cleanup(func() { agentsTriggerFile = old })

	if err := agentsTriggerCmd.RunE(agentsTriggerCmd, nil); err != nil {
		t.Fatalf("env webhook: %v", err)
	}
	if ts.posts != 1 || ts.lookups != 0 {
		t.Fatalf("posts %d lookups %d", ts.posts, ts.lookups)
	}

	// An agent id wins over the environment: it is looked up with the token.
	globalToken, globalURL = cmdTestToken, srv.URL
	if err := agentsTriggerCmd.RunE(agentsTriggerCmd, []string{"agent-1"}); err != nil {
		t.Fatalf("agent id: %v", err)
	}
	if ts.posts != 2 || ts.lookups != 1 || ts.lookupAgent != "agent-1" {
		t.Errorf("posts %d lookups %d agent %q", ts.posts, ts.lookups, ts.lookupAgent)
	}
}

func TestAgentsTriggerArgs(t *testing.T) {
	if err := agentsTriggerCmd.Args(agentsTriggerCmd, []string{"a", "b"}); exitCodeFor(err) != exitUsage {
		t.Errorf("two agent ids: %v", err)
	}
}
