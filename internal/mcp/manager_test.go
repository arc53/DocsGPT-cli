package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/mcp"
	"github.com/arc53/DocsGPT-cli/internal/mcp/mcptest"
)

func TestMain(m *testing.M) { mcptest.Main(m) }

func start(t *testing.T, servers ...mcp.Server) *mcp.Manager {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := mcp.Start(servers, "test")
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	return m
}

func call(t *testing.T, m *mcp.Manager, name string, args map[string]any) (string, error) {
	t.Helper()
	tool := m.Lookup(name)
	if tool == nil {
		t.Fatalf("no tool %s", name)
	}
	res, err := m.Call(context.Background(), tool, args)
	if err != nil {
		return "", err
	}
	text, _ := mcp.Result(res)
	return text, nil
}

func TestManager(t *testing.T) {
	m := start(t, mcptest.Server("test"))
	st := m.Status()
	if len(st) != 1 || !st[0].Running || st[0].Err != nil || st[0].Instructions != "Call echo to echo." {
		t.Fatalf("status %+v", st)
	}
	// The banner on stdout went to the log, not into the protocol.
	if got := strings.Join(st[0].Stderr, "\n"); !strings.Contains(got, "hello from stderr") || !strings.Contains(got, "stdout: Booting") {
		t.Errorf("stderr tail %q", got)
	}
	defs := m.Definitions()
	var names []string
	for _, d := range defs {
		names = append(names, d.Function.Name)
	}
	if got := strings.Join(names, " "); !strings.Contains(got, "mcp__test__echo") || !strings.Contains(got, "mcp__test__crash") {
		t.Fatalf("definitions %s", got)
	}
	if !m.Lookup("mcp__test__echo").ReadOnly() || m.Lookup("mcp__test__fail").ReadOnly() {
		t.Error("read-only hints")
	}

	if got, err := call(t, m, "mcp__test__echo", map[string]any{"text": "hi"}); got != "echo: hi" || err != nil {
		t.Errorf("echo = %q, %v", got, err)
	}
	res, err := m.Call(context.Background(), m.Lookup("mcp__test__image"), nil)
	if err != nil {
		t.Fatal(err)
	}
	text, images := mcp.Result(res)
	if len(images) != 1 || !strings.HasPrefix(images[0].ImageURL.URL, "data:image/png;base64,") || !strings.Contains(text, "[Image 1 (image/png") {
		t.Errorf("image result %q %+v", text, images)
	}
	res, err = m.Call(context.Background(), m.Lookup("mcp__test__fail"), nil)
	if err != nil || !res.IsError {
		t.Errorf("fail: %v %+v", err, res)
	}

	// A call cancelled stops waiting at once.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	begin := time.Now()
	if _, err := m.Call(ctx, m.Lookup("mcp__test__slow"), nil); err == nil || time.Since(begin) > 5*time.Second {
		t.Errorf("slow call: %v after %v", err, time.Since(begin))
	}

	// The server dies: the call fails, saying what it wrote; the next call
	// starts it again.
	if _, err := call(t, m, "mcp__test__crash", nil); err == nil || !strings.Contains(err.Error(), "crashing") {
		t.Errorf("crash: %v", err)
	}
	if got, err := call(t, m, "mcp__test__echo", map[string]any{"text": "again"}); got != "echo: again" || err != nil {
		t.Errorf("after the crash: %q, %v", got, err)
	}
}

func TestManagerFailures(t *testing.T) {
	missing := mcp.Server{Name: "missing", Command: "docsgpt-no-such-program"}
	exits := mcptest.Server("exits")
	exits.Args = append(exits.Args, "-test.bench=x") // the server ignores flags…
	exits.Env = map[string]string{}                  // …but without its variable it is just a test binary that exits
	m := start(t, missing, exits, mcptest.Server("ok"))
	st := m.Status()
	if st[0].Err == nil || !strings.Contains(st[0].Err.Error(), "docsgpt-no-such-program") {
		t.Errorf("missing program: %v", st[0].Err)
	}
	if st[1].Err == nil {
		t.Error("a program that is no server started")
	}
	if st[2].Err != nil || !st[2].Running {
		t.Errorf("the good one: %+v", st[2])
	}
	for _, d := range m.Definitions() {
		if !strings.HasPrefix(d.Function.Name, "mcp__ok__") {
			t.Errorf("tool of a failed server: %s", d.Function.Name)
		}
	}
}
