package host

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// ServiceUnitName is the systemd unit name (no extension) for the host daemon.
const ServiceUnitName = "docsgpt-cli-host"

// ServiceMode is user (~/.config/systemd/user) or system (/etc/systemd/system).
type ServiceMode int

const (
	ServiceModeUser ServiceMode = iota
	ServiceModeSystem
)

func (m ServiceMode) String() string {
	if m == ServiceModeSystem {
		return "system"
	}
	return "user"
}

// ErrUnsupportedOS is returned by service operations on platforms without a
// supported service backend (anything other than Linux/systemd,
// macOS/launchd, or Windows/Task Scheduler).
var ErrUnsupportedOS = errors.New(
	"install-service supports linux (systemd), macOS (launchd), and " +
		"Windows (Task Scheduler) only; run `docsgpt-cli host` manually " +
		"on this platform",
)

// ResolveServiceMode decides the install mode and runtime user for
// install-service from the invocation context. Precedence: an explicit
// --system flag is respected; otherwise root defaults to system mode and
// non-root to user mode (the daemon then runs as the invoking user).
//
// In system mode with no explicit --user, it prefers $SUDO_USER (the human
// who sudo'd in) and falls back to root. ``note`` is a one-line, possibly
// empty, message for the user explaining an automatic choice.
func ResolveServiceMode(isRoot, systemFlag bool, userFlag, sudoUser string) (mode ServiceMode, runUser, note string) {
	switch {
	case systemFlag:
		// User asked for system mode explicitly; no auto-select note.
		mode = ServiceModeSystem
	case isRoot:
		mode = ServiceModeSystem
		note = "Running as root — installing as a system service " +
			"(user services require an active login session)."
	default:
		return ServiceModeUser, "", ""
	}

	// System mode only past this point. User mode runs as the invoking
	// user implicitly, so runUser is irrelevant there.
	if userFlag != "" {
		runUser = userFlag
		return mode, runUser, note
	}
	if sudoUser != "" && sudoUser != "root" {
		runUser = sudoUser
		return mode, runUser, note
	}
	runUser = "root"
	warn := "No --user given; the daemon will run as root. " +
		"Re-run with --user <name> to run as a less-privileged user."
	if note == "" {
		note = warn
	} else {
		note += "\n" + warn
	}
	return mode, runUser, note
}

// ServiceUnitPath returns the absolute filesystem location of the unit file.
func ServiceUnitPath(mode ServiceMode) (string, error) {
	if mode == ServiceModeSystem {
		return filepath.Join("/etc/systemd/system", ServiceUnitName+".service"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", ServiceUnitName+".service"), nil
}

// RenderServiceUnit returns the systemd unit file content for the given mode.
//
// ``exec`` is the absolute path to the docsgpt-cli binary. For
// ServiceModeSystem, ``systemUser`` is the OS user the service should run
// as and must be non-empty; for ServiceModeUser it is ignored.
func RenderServiceUnit(mode ServiceMode, exec, systemUser string) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=DocsGPT CLI host daemon\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n")
	b.WriteString("\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s host\n", exec)
	// Restart only on failure. A revoke exits 0, so on-failure already
	// leaves a revoked device stopped without a RestartPreventExitStatus.
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=10\n")
	if mode == ServiceModeSystem && systemUser != "" {
		fmt.Fprintf(&b, "User=%s\n", systemUser)
	}
	b.WriteString("\n")
	b.WriteString("[Install]\n")
	if mode == ServiceModeSystem {
		b.WriteString("WantedBy=multi-user.target\n")
	} else {
		b.WriteString("WantedBy=default.target\n")
	}
	return b.String()
}

// ResolveExecutable returns the symlink-evaluated absolute path to the
// currently-running CLI binary. The unit file should not reference a
// volatile symlink such as a freshly-built /tmp path.
func ResolveExecutable() (string, error) {
	exec, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("os.Executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exec)
	if err != nil {
		// Fall back to the unresolved path; the unit still works.
		resolved = exec
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return resolved, nil
	}
	return abs, nil
}

// IsLinux reports whether the current platform is Linux.
func IsLinux() bool { return runtime.GOOS == "linux" }

// IsDarwin reports whether the current platform is macOS.
func IsDarwin() bool { return runtime.GOOS == "darwin" }

// ServiceInstallSupported reports whether install-service has a service
// backend for the current platform (systemd on Linux, launchd on macOS,
// Task Scheduler on Windows).
func ServiceInstallSupported() bool { return IsLinux() || IsDarwin() || IsWindows() }

// SystemctlArgs returns the args prefix to invoke systemctl for the given mode.
func SystemctlArgs(mode ServiceMode) []string {
	if mode == ServiceModeSystem {
		return nil
	}
	return []string{"--user"}
}

// JournalArgs returns the args prefix to inspect logs via journalctl.
func JournalArgs(mode ServiceMode) []string {
	if mode == ServiceModeSystem {
		return []string{"-u", ServiceUnitName}
	}
	return []string{"--user", "-u", ServiceUnitName}
}

// runSystemctl shells out to ``systemctl`` with the right scope. Returns
// stderr on failure so the caller can pass it through to the user.
func runSystemctl(mode ServiceMode, args ...string) error {
	full := append(SystemctlArgs(mode), args...)
	cmd := exec.Command("systemctl", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %s", strings.Join(full, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// CurrentSystemUser returns the OS user the CLI is running as. Used as a
// sensible default for ``--system --user``.
func CurrentSystemUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("user.Current: %w", err)
	}
	return u.Username, nil
}

// WriteUnitFile writes the unit file at ``path`` with 0644 permissions,
// creating any missing parent directories with mode 0755 (system) or
// 0700 (user, under ~).
func WriteUnitFile(path, content string, mode ServiceMode) error {
	dirMode := os.FileMode(0755)
	if mode == ServiceModeUser {
		dirMode = 0700
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("mkdir unit dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	return nil
}

// DaemonReload runs ``systemctl [--user] daemon-reload``.
func DaemonReload(mode ServiceMode) error { return runSystemctl(mode, "daemon-reload") }

// EnableNow runs ``systemctl [--user] enable --now <unit>``.
func EnableNow(mode ServiceMode) error {
	return runSystemctl(mode, "enable", "--now", ServiceUnitName)
}

// DisableNow runs ``systemctl [--user] disable --now <unit>``.
func DisableNow(mode ServiceMode) error {
	return runSystemctl(mode, "disable", "--now", ServiceUnitName)
}

// DetectInstalledMode looks for an existing unit file and reports whether
// it lives in the user-scope or system-scope path. Returns ``found=false``
// when neither file exists.
func DetectInstalledMode() (mode ServiceMode, found bool) {
	if path, err := ServiceUnitPath(ServiceModeUser); err == nil {
		if _, err := os.Stat(path); err == nil {
			return ServiceModeUser, true
		}
	}
	sysPath, _ := ServiceUnitPath(ServiceModeSystem)
	if _, err := os.Stat(sysPath); err == nil {
		return ServiceModeSystem, true
	}
	return ServiceModeUser, false
}
