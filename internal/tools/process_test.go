package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// helperEnv makes the test binary a background command of the tests (see
// helperProcess), which runs the same everywhere.
const helperEnv = "DOCSGPT_PROCTEST"

// helperProcess acts out mode: "print:N:ms:code" prints N lines ms apart
// and exits with code; "sleep" sleeps; "spawn" starts a sleeping child,
// prints its pid and sleeps.
func helperProcess(mode string) {
	f := strings.Split(mode, ":")
	switch f[0] {
	case "print":
		n, _ := strconv.Atoi(f[1])
		ms, _ := strconv.Atoi(f[2])
		code, _ := strconv.Atoi(f[3])
		for i := 1; i <= n; i++ {
			fmt.Printf("line %d\n", i)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		fmt.Fprintln(os.Stderr, "to stderr")
		os.Exit(code)
	case "sleep":
		fmt.Println("sleeping")
		time.Sleep(time.Minute)
	case "spawn":
		cmd := exec.Command(os.Args[0], "-test.run=XXX_NONE")
		cmd.Env = append(os.Environ(), helperEnv+"=sleep")
		cmd.Start()
		fmt.Println(cmd.Process.Pid)
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

// helper is the shell command that runs the test binary as mode. (No
// -test.run=^$: cmd.exe reads ^ as an escape.)
func helper(mode string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`set "%s=%s" && "%s" -test.run=XXX_NONE`, helperEnv, mode, os.Args[0])
	}
	return fmt.Sprintf(`%s='%s' '%s' -test.run=XXX_NONE`, helperEnv, mode, os.Args[0])
}

// testProcesses gives the test a home, and Processes whose wake-ups it
// counts.
func testProcesses(t *testing.T) (*Processes, *atomic.Int32) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var woken atomic.Int32
	ps := NewProcesses(func() { woken.Add(1) })
	t.Cleanup(func() { ps.Close() })
	return ps, &woken
}

// events waits until n events wait (or 15s), and returns them.
func events(t *testing.T, ps *Processes, n int) []Event {
	t.Helper()
	var got []Event
	deadline := time.Now().Add(15 * time.Second)
	for len(got) < n && time.Now().Before(deadline) {
		got = append(got, ps.Drain()...)
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) < n {
		t.Fatalf("%d events, want %d: %+v", len(got), n, got)
	}
	return got
}

func TestProcessExitEvent(t *testing.T) {
	ps, woken := testProcesses(t)
	info, err := ps.Start(helper("print:3:0:2"), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "p1" || !info.Running {
		t.Fatalf("info = %+v", info)
	}
	e := events(t, ps, 1)[0]
	for _, want := range []string{"p1 · ", "exited with code 2", "line 3", "to stderr"} {
		if !strings.Contains(e.Text, want) {
			t.Errorf("event lacks %q:\n%s", want, e.Text)
		}
	}
	time.Sleep(2 * eventDelay)
	if woken.Load() != 1 {
		t.Errorf("woken %d times, want 1", woken.Load())
	}
	if i := ps.List()[0]; i.Running || i.Exit != 2 || ps.Running() != 0 {
		t.Errorf("after exit: %+v", i)
	}
	if fi, err := os.Stat(info.Log); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("log: %v %v", fi, err)
	}
	text := ps.Format([]Event{e})
	for _, want := range []string{"<local_process_event>", "not typed by the user", "No background process runs now.", "</local_process_event>"} {
		if !strings.Contains(text, want) {
			t.Errorf("Format lacks %q", want)
		}
	}
}

func TestMonitorEvents(t *testing.T) {
	ps, _ := testProcesses(t)
	if _, err := ps.Monitor(helper("print:5:0:0"), "", "test lines", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	got := events(t, ps, 2)
	if !strings.Contains(got[0].Text, `monitor "test lines": 5 lines`) || !strings.Contains(got[0].Text, "\nline 5") || strings.Contains(got[0].Text, "stderr") {
		t.Errorf("lines event:\n%s", got[0].Text)
	}
	if !strings.Contains(got[1].Text, "exited with code 0") || !strings.Contains(got[1].Text, "after 1 event") {
		t.Errorf("exit event:\n%s", got[1].Text)
	}
}

func TestMonitorFloodAndExpiry(t *testing.T) {
	ps, _ := testProcesses(t)
	if _, err := ps.Monitor(helper(fmt.Sprintf("print:%d:0:0", floodLines+100)), "", "flood", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	var all string
	for _, e := range events(t, ps, 2) {
		all += e.Text + "\n"
	}
	if !strings.Contains(all, "more lines, in its log") || !strings.Contains(all, fmt.Sprintf("more than %d lines", floodLines)) {
		t.Errorf("flood events:\n%s", all)
	}

	if _, err := ps.Monitor(helper("sleep"), "", "quiet", 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	got := events(t, ps, 2) // "sleeping", then the expiry
	if !strings.Contains(got[len(got)-1].Text, "expired after") {
		t.Errorf("expiry: %+v", got)
	}
}

func TestStopAndClose(t *testing.T) {
	ps, woken := testProcesses(t)
	a, _ := ps.Start(helper("sleep"), "", 0)
	b, _ := ps.Start(helper("sleep"), "", 0)
	c, _ := ps.Start(helper("sleep"), "", 0)
	if ps.Running() != 3 {
		t.Fatalf("running = %d", ps.Running())
	}
	// The model stops one: it knows, no event.
	if i, err := ps.Stop(a.ID, false); err != nil || i.Running || i.How != "stopped" {
		t.Errorf("Stop = %+v, %v", i, err)
	}
	// The user stops one: the model is told, with the next message.
	ps.Stop(b.ID, true)
	time.Sleep(3 * eventDelay)
	got := ps.Drain()
	if len(got) != 1 || !strings.Contains(got[0].Text, "stopped by the user") || woken.Load() != 0 {
		t.Errorf("events %+v, woken %d", got, woken.Load())
	}
	if _, err := ps.Stop("p9", false); err == nil || !strings.Contains(err.Error(), "no background process") {
		t.Errorf("unknown id: %v", err)
	}
	if n := ps.Close(); n != 1 || ps.List()[2].Running {
		t.Errorf("Close stopped %d, %+v", n, ps.List()[2])
	}
	if len(ps.Drain()) != 0 {
		t.Error("events after Close")
	}
	if _, err := ps.Start(helper("sleep"), "", 0); err == nil {
		t.Error("started after Close")
	}
	_ = c
}

func TestStartTimeout(t *testing.T) {
	ps, _ := testProcesses(t)
	ps.Start(helper("sleep"), "", 200*time.Millisecond)
	if e := events(t, ps, 1)[0]; !strings.Contains(e.Text, "timed out after") {
		t.Errorf("event: %s", e.Text)
	}
}

func TestMoveToBackground(t *testing.T) {
	ps, _ := testProcesses(t)
	move := make(chan struct{})
	var out tailBuffer
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(move)
	}()
	r, err := runForeground(context.Background(), helper("print:6:150:0"), "", time.Minute, &out, move)
	if err != errMoved {
		t.Fatalf("err = %v", err)
	}
	info, err := ps.Adopt(r, "the command", time.Now().Add(-time.Minute), func() string { return string(out.buf) })
	if err != nil {
		t.Fatal(err)
	}
	e := events(t, ps, 1)[0]
	if !strings.Contains(e.Text, "exited with code 0 after 1m") {
		t.Errorf("event: %s", e.Text)
	}
	log, _ := os.ReadFile(info.Log)
	if !strings.Contains(string(log), "line 1\n") || !strings.Contains(string(log), "line 6\n") {
		t.Errorf("log lacks the output before or after the move:\n%s", log)
	}
}

func TestSessionProcessTools(t *testing.T) {
	ps, _ := testProcesses(t)
	t.Chdir(t.TempDir())
	u := &recordingUI{choice: "approve"}
	s := &Session{UI: u, Timeout: time.Minute, Processes: ps}
	call := func(name, args string) string {
		return s.Handle(context.Background(), func() {}, docsgpt.ToolCall{Function: docsgpt.FunctionCall{Name: name, Arguments: args}}).Content
	}
	q := func(v string) string { b := strconv.Quote(v); return b }
	got := call("run_command", `{"command":`+q(helper("sleep"))+`,"run_in_background":"true"}`)
	if !strings.Contains(got, "Started p1 in the background") || !strings.Contains(got, "<local_process_event>") {
		t.Fatalf("start: %s", got)
	}
	if got := call("local_output", `{"id":"p1"}`); !strings.Contains(got, "p1 · running") {
		t.Errorf("output: %s", got)
	}
	if got := call("local_output", `{}`); !strings.Contains(got, "p1 · running") {
		t.Errorf("list: %s", got)
	}
	if got := call("local_monitor", `{"command":`+q(helper("print:2:0:0"))+`,"description":"two lines","timeout":5}`); !strings.Contains(got, `Started monitor p2 ("two lines")`) {
		t.Errorf("monitor: %s", got)
	}
	if got := call("local_stop", `{"id":"p1"}`); got != "Stopped p1 and everything it started." {
		t.Errorf("stop: %s", got)
	}
	if got := call("local_stop", `{"id":"p7"}`); !strings.Contains(got, "no background process") {
		t.Errorf("unknown: %s", got)
	}
	// A command that would detach is refused without asking.
	asked := len(u.offered)
	detach := "sleep 100 &"
	if runtime.GOOS == "windows" {
		detach = "start /b ping -n 100 127.0.0.1"
	}
	if got := call("run_command", `{"command":`+q(detach)+`}`); !strings.Contains(got, "run_in_background instead") || len(u.offered) != asked {
		t.Errorf("detach: %s", got)
	}
	if got := call("run_command", `{"command":"sleep 30"}`); runtime.GOOS != "windows" && !strings.Contains(got, "sleeping 30s blocks") {
		t.Errorf("sleep: %s", got)
	}
	// Without Processes, the tools are not there.
	s.Processes = nil
	if got := call("local_output", `{}`); !strings.Contains(got, "unknown tool") {
		t.Errorf("without processes: %s", got)
	}
}

func TestGuards(t *testing.T) {
	if runtime.GOOS == "windows" {
		for cmd, want := range map[string]string{
			"start /b npm run dev": "start", "npm start": "", "echo a & echo b": "",
			`powershell -c "Start-Process node"`: "Start-Process",
		} {
			if got := detaches(cmd); got != want {
				t.Errorf("detaches(%q) = %q, want %q", cmd, got, want)
			}
		}
		for cmd, want := range map[string]time.Duration{
			"timeout /t 30": 30 * time.Second, "timeout 5": 0, `powershell -c "Start-Sleep -Seconds 20"`: 20 * time.Second,
			"Start-Sleep -Milliseconds 500": 0,
		} {
			if got := longSleep(cmd); got != want {
				t.Errorf("longSleep(%q) = %v, want %v", cmd, got, want)
			}
		}
		return
	}
	for cmd, want := range map[string]string{
		"npm run dev &":                     "&",
		"npm run dev & sleep 2; curl x":     "&",
		"a & b & wait":                      "",
		"make 2>&1 | tee log":               "",
		"go test ./... &> out.txt":          "",
		"nohup ./server":                    "nohup",
		"FOO=1 setsid ./x":                  "setsid",
		"./x; disown":                       "disown",
		"echo 'a & b' \"c & d\"":            "",
		"grep nohup README.md && echo done": "",
		"echo a && echo b || true":          "",
	} {
		if got := detaches(cmd); got != want {
			t.Errorf("detaches(%q) = %q, want %q", cmd, got, want)
		}
	}
	for cmd, want := range map[string]time.Duration{
		"sleep 30":                           30 * time.Second,
		"sleep 1m && curl localhost":         time.Minute,
		"sleep 5":                            0,
		"sleep 10":                           0,
		"until curl -s x; do sleep 60; done": 0,
		"echo hi; sleep 120":                 0,
		"sleep 1 1 9":                        11 * time.Second,
	} {
		if got := longSleep(cmd); got != want {
			t.Errorf("longSleep(%q) = %v, want %v", cmd, got, want)
		}
	}
}
