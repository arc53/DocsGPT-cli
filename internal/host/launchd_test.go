package host

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRenderLaunchdPlistAgent(t *testing.T) {
	plist := RenderLaunchdPlist(
		ServiceModeUser, "/usr/local/bin/docsgpt-cli", "",
		"/Users/alice/.docsgpt/host.log",
	)
	checks := []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		"<!DOCTYPE plist PUBLIC",
		`<plist version="1.0">`,
		"<key>Label</key>",
		"<string>com.arc53.docsgpt-cli-host</string>",
		"<key>ProgramArguments</key>",
		"<string>/usr/local/bin/docsgpt-cli</string>",
		"<string>host</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<key>SuccessfulExit</key>",
		"<false/>",
		"<key>StandardOutPath</key>",
		"<string>/Users/alice/.docsgpt/host.log</string>",
		"<key>StandardErrorPath</key>",
	}
	for _, want := range checks {
		if !strings.Contains(plist, want) {
			t.Errorf("agent plist missing %q\nfull plist:\n%s", want, plist)
		}
	}
	// LaunchAgents run as the invoking user; UserName must be absent.
	if strings.Contains(plist, "<key>UserName</key>") {
		t.Errorf("agent plist must not contain UserName\nfull plist:\n%s", plist)
	}
}

func TestRenderLaunchdPlistDaemon(t *testing.T) {
	plist := RenderLaunchdPlist(
		ServiceModeSystem, "/usr/local/bin/docsgpt-cli", "alice",
		"/Users/alice/.docsgpt/host.log",
	)
	checks := []string{
		"<key>Label</key>",
		"<string>com.arc53.docsgpt-cli-host</string>",
		"<key>ProgramArguments</key>",
		"<string>/usr/local/bin/docsgpt-cli</string>",
		"<string>host</string>",
		"<key>UserName</key>",
		"<string>alice</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<key>SuccessfulExit</key>",
		"<false/>",
		"<key>StandardOutPath</key>",
		"<string>/Users/alice/.docsgpt/host.log</string>",
		"<key>StandardErrorPath</key>",
	}
	for _, want := range checks {
		if !strings.Contains(plist, want) {
			t.Errorf("daemon plist missing %q\nfull plist:\n%s", want, plist)
		}
	}
}

// TestRenderLaunchdPlistDaemonOmitsEmptyUser guards that a LaunchDaemon with
// no runtime user (root fallback) emits no UserName key, so launchd runs it
// as root rather than failing on an empty user.
func TestRenderLaunchdPlistDaemonOmitsEmptyUser(t *testing.T) {
	plist := RenderLaunchdPlist(
		ServiceModeSystem, "/opt/bin/docsgpt-cli", "",
		"/var/root/.docsgpt/host.log",
	)
	if strings.Contains(plist, "<key>UserName</key>") {
		t.Errorf("empty UserName must be omitted entirely, got:\n%s", plist)
	}
}

// TestRenderLaunchdPlistStdoutStderrSameFile asserts both stdout and stderr
// point at the configured log path.
func TestRenderLaunchdPlistStdoutStderrSameFile(t *testing.T) {
	logPath := "/Users/bob/.docsgpt/host.log"
	plist := RenderLaunchdPlist(ServiceModeUser, "/usr/local/bin/docsgpt-cli", "", logPath)
	if n := strings.Count(plist, "<string>"+logPath+"</string>"); n != 2 {
		t.Errorf("expected log path twice (out+err), got %d\nplist:\n%s", n, plist)
	}
}

func TestResolveLaunchdMode(t *testing.T) {
	cases := []struct {
		name      string
		isRoot    bool
		system    bool
		userFlag  string
		sudoUser  string
		wantMode  ServiceMode
		wantUser  string
		noteEmpty bool
		noteHas   string
	}{
		{
			name:      "non-root no --system installs a LaunchAgent",
			isRoot:    false,
			wantMode:  ServiceModeUser,
			wantUser:  "",
			noteEmpty: true,
		},
		{
			name:     "root no --system auto-selects LaunchDaemon, root fallback",
			isRoot:   true,
			wantMode: ServiceModeSystem,
			wantUser: "root",
			noteHas:  "Running as root",
		},
		{
			name:     "root no --system resolves runtime user from SUDO_USER",
			isRoot:   true,
			sudoUser: "alice",
			wantMode: ServiceModeSystem,
			wantUser: "alice",
			noteHas:  "Running as root",
		},
		{
			name:     "root no --system falls back to root when SUDO_USER is root",
			isRoot:   true,
			sudoUser: "root",
			wantMode: ServiceModeSystem,
			wantUser: "root",
			noteHas:  "No --user given",
		},
		{
			name:     "explicit --user wins over SUDO_USER",
			isRoot:   true,
			userFlag: "svc",
			sudoUser: "alice",
			wantMode: ServiceModeSystem,
			wantUser: "svc",
			noteHas:  "Running as root",
		},
		{
			name:      "explicit --system prints no auto-select note",
			isRoot:    true,
			system:    true,
			userFlag:  "svc",
			wantMode:  ServiceModeSystem,
			wantUser:  "svc",
			noteEmpty: true,
		},
		{
			name:     "explicit --system with no user warns and uses root",
			isRoot:   true,
			system:   true,
			wantMode: ServiceModeSystem,
			wantUser: "root",
			noteHas:  "No --user given",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, runUser, note := ResolveLaunchdMode(tc.isRoot, tc.system, tc.userFlag, tc.sudoUser)
			if mode != tc.wantMode {
				t.Errorf("mode = %s, want %s", mode, tc.wantMode)
			}
			if runUser != tc.wantUser {
				t.Errorf("runUser = %q, want %q", runUser, tc.wantUser)
			}
			if tc.noteEmpty && note != "" {
				t.Errorf("expected empty note, got %q", note)
			}
			if tc.noteHas != "" && !strings.Contains(note, tc.noteHas) {
				t.Errorf("note %q does not contain %q", note, tc.noteHas)
			}
		})
	}
}

// TestResolveLaunchdModeExplicitSystemNoAutoNote guards that an explicit
// --system never emits the "Running as root" auto-select line, mirroring the
// systemd contract.
func TestResolveLaunchdModeExplicitSystemNoAutoNote(t *testing.T) {
	_, _, note := ResolveLaunchdMode(true, true, "", "")
	if strings.Contains(note, "Running as root") {
		t.Errorf("explicit --system must not print the auto-select note, got %q", note)
	}
}

func TestLaunchdDomainTarget(t *testing.T) {
	if got := LaunchdDomainTarget(ServiceModeSystem); got != "system" {
		t.Errorf("daemon domain = %q, want %q", got, "system")
	}
	wantAgent := fmt.Sprintf("gui/%d", os.Getuid())
	if got := LaunchdDomainTarget(ServiceModeUser); got != wantAgent {
		t.Errorf("agent domain = %q, want %q", got, wantAgent)
	}
}

func TestLaunchdServiceTarget(t *testing.T) {
	if got := LaunchdServiceTarget(ServiceModeSystem); got != "system/"+LaunchdLabel {
		t.Errorf("daemon service target = %q", got)
	}
	wantAgent := fmt.Sprintf("gui/%d/%s", os.Getuid(), LaunchdLabel)
	if got := LaunchdServiceTarget(ServiceModeUser); got != wantAgent {
		t.Errorf("agent service target = %q, want %q", got, wantAgent)
	}
}

func TestLaunchdBootstrapCmd(t *testing.T) {
	got := LaunchdBootstrapCmd(ServiceModeSystem, "/Library/LaunchDaemons/x.plist")
	want := "launchctl bootstrap system /Library/LaunchDaemons/x.plist"
	if got != want {
		t.Errorf("bootstrap cmd = %q, want %q", got, want)
	}
}

func TestLaunchdDaemonPlistPath(t *testing.T) {
	got, err := LaunchdPlistPath(ServiceModeSystem, "alice")
	if err != nil {
		t.Fatalf("LaunchdPlistPath: %v", err)
	}
	want := "/Library/LaunchDaemons/com.arc53.docsgpt-cli-host.plist"
	if got != want {
		t.Errorf("daemon plist path = %q, want %q", got, want)
	}
}

func TestLaunchdAgentPlistPathUnderHome(t *testing.T) {
	got, err := LaunchdPlistPath(ServiceModeUser, "")
	if err != nil {
		t.Fatalf("LaunchdPlistPath: %v", err)
	}
	if !strings.HasSuffix(got, "/Library/LaunchAgents/com.arc53.docsgpt-cli-host.plist") {
		t.Errorf("agent plist path = %q, want it to end under ~/Library/LaunchAgents", got)
	}
}

func TestWriteLaunchdPlistCreatesParents(t *testing.T) {
	dir := t.TempDir()
	target := dir + "/nested/LaunchAgents/com.arc53.docsgpt-cli-host.plist"
	content := RenderLaunchdPlist(ServiceModeUser, "/usr/local/bin/docsgpt-cli", "", dir+"/host.log")
	if err := WriteLaunchdPlist(target, content, ServiceModeUser); err != nil {
		t.Fatalf("WriteLaunchdPlist: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read written plist: %v", err)
	}
	if string(data) != content {
		t.Errorf("on-disk content differs from rendered plist")
	}
}
