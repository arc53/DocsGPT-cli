package host

import (
	"os"
	"strings"
	"testing"
)

func TestRenderServiceUnitUserMode(t *testing.T) {
	unit := RenderServiceUnit(ServiceModeUser, "/usr/local/bin/docsgpt-cli", "")
	checks := []string{
		"[Unit]",
		"Description=DocsGPT CLI host daemon",
		"After=network-online.target",
		"Wants=network-online.target",
		"[Service]",
		"Type=simple",
		"ExecStart=/usr/local/bin/docsgpt-cli host",
		"Restart=on-failure",
		"RestartSec=10",
		"[Install]",
		"WantedBy=default.target",
	}
	for _, want := range checks {
		if !strings.Contains(unit, want) {
			t.Errorf("user unit missing %q\nfull unit:\n%s", want, unit)
		}
	}
	// RestartPreventExitStatus is gone: revoke exits 0, so Restart=on-failure
	// already leaves a revoked daemon stopped.
	if strings.Contains(unit, "RestartPreventExitStatus") {
		t.Errorf("unit must not contain RestartPreventExitStatus\nfull unit:\n%s", unit)
	}
	// User-mode units MUST NOT pin User=; the service runs as the
	// invoking user implicitly.
	if strings.Contains(unit, "User=") {
		t.Errorf("user unit must not contain User= directive\nfull unit:\n%s", unit)
	}
	// User-mode WantedBy must be default.target (not multi-user.target).
	if strings.Contains(unit, "WantedBy=multi-user.target") {
		t.Errorf("user unit must not target multi-user.target\nfull unit:\n%s", unit)
	}
}

func TestRenderServiceUnitSystemMode(t *testing.T) {
	unit := RenderServiceUnit(
		ServiceModeSystem, "/usr/local/bin/docsgpt-cli", "alice",
	)
	checks := []string{
		"[Unit]",
		"[Service]",
		"ExecStart=/usr/local/bin/docsgpt-cli host",
		"User=alice",
		"Restart=on-failure",
		"[Install]",
		"WantedBy=multi-user.target",
	}
	for _, want := range checks {
		if !strings.Contains(unit, want) {
			t.Errorf("system unit missing %q\nfull unit:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "WantedBy=default.target") {
		t.Errorf("system unit must not target default.target\nfull unit:\n%s", unit)
	}
}

func TestRenderServiceUnitSystemModeOmitsEmptyUser(t *testing.T) {
	unit := RenderServiceUnit(ServiceModeSystem, "/opt/bin/docsgpt-cli", "")
	if strings.Contains(unit, "User=") {
		t.Errorf("empty User must be omitted entirely, got:\n%s", unit)
	}
}

func TestJournalArgs(t *testing.T) {
	if got := JournalArgs(ServiceModeUser); strings.Join(got, " ") != "--user -u docsgpt-cli-host" {
		t.Errorf("user journal args wrong: %v", got)
	}
	if got := JournalArgs(ServiceModeSystem); strings.Join(got, " ") != "-u docsgpt-cli-host" {
		t.Errorf("system journal args wrong: %v", got)
	}
}

func TestSystemctlArgs(t *testing.T) {
	if got := SystemctlArgs(ServiceModeUser); strings.Join(got, " ") != "--user" {
		t.Errorf("user systemctl args wrong: %v", got)
	}
	if got := SystemctlArgs(ServiceModeSystem); len(got) != 0 {
		t.Errorf("system systemctl args should be empty, got %v", got)
	}
}

func TestWriteUnitFileCreatesParents(t *testing.T) {
	dir := t.TempDir()
	target := dir + "/nested/sub/unit.service"
	content := RenderServiceUnit(ServiceModeUser, "/usr/local/bin/docsgpt-cli", "")
	if err := WriteUnitFile(target, content, ServiceModeUser); err != nil {
		t.Fatalf("WriteUnitFile: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read written unit: %v", err)
	}
	if string(data) != content {
		t.Errorf("on-disk content differs from rendered unit")
	}
}

func TestResolveServiceMode(t *testing.T) {
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
			name:      "non-root no --system defaults to user mode",
			isRoot:    false,
			wantMode:  ServiceModeUser,
			wantUser:  "",
			noteEmpty: true,
		},
		{
			name:     "root no --system auto-selects system mode",
			isRoot:   true,
			wantMode: ServiceModeSystem,
			// No --user and no SUDO_USER -> root fallback.
			wantUser: "root",
			noteHas:  "Running as root",
		},
		{
			name:     "root no --system resolves runtime user from SUDO_USER",
			isRoot:   true,
			sudoUser: "pi",
			wantMode: ServiceModeSystem,
			wantUser: "pi",
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
			sudoUser: "pi",
			wantMode: ServiceModeSystem,
			wantUser: "svc",
			noteHas:  "Running as root",
		},
		{
			name:      "explicit --system as root prints no auto-select note",
			isRoot:    true,
			system:    true,
			userFlag:  "svc",
			wantMode:  ServiceModeSystem,
			wantUser:  "svc",
			noteEmpty: true,
		},
		{
			name:     "explicit --system as root with no user warns and uses root",
			isRoot:   true,
			system:   true,
			wantMode: ServiceModeSystem,
			wantUser: "root",
			noteHas:  "No --user given",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, runUser, note := ResolveServiceMode(tc.isRoot, tc.system, tc.userFlag, tc.sudoUser)
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

// TestResolveServiceModeExplicitSystemNoAutoNote guards the contract that
// an explicit --system never emits the "Running as root" auto-select line
// (the user asked for system mode), even though it may still warn about a
// missing --user.
func TestResolveServiceModeExplicitSystemNoAutoNote(t *testing.T) {
	_, _, note := ResolveServiceMode(true, true, "", "")
	if strings.Contains(note, "Running as root") {
		t.Errorf("explicit --system must not print the auto-select note, got %q", note)
	}
}

func TestResolveExecutableNonEmpty(t *testing.T) {
	got, err := ResolveExecutable()
	if err != nil {
		t.Fatalf("ResolveExecutable error: %v", err)
	}
	if got == "" {
		t.Fatal("ResolveExecutable returned empty path")
	}
}
