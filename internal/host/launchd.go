package host

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// LaunchdLabel is the launchd job label for the host daemon. Doubles as the
// plist basename (Label + ".plist").
const LaunchdLabel = "com.arc53.docsgpt-cli-host"

// launchdAgentDir / launchdDaemonDir are the standard plist directories.
// LaunchAgents (per-user, runs on login) for ServiceModeUser; LaunchDaemons
// (system-wide, runs at boot) for ServiceModeSystem.
const (
	launchdDaemonDir = "/Library/LaunchDaemons"
	launchdAgentSub  = "Library/LaunchAgents"
)

// ResolveLaunchdMode decides the launchd install mode and runtime user,
// parallel to ResolveServiceMode but with launchd terminology in the note.
// Non-root installs a LaunchAgent (runs as the invoking user, on login);
// root (or explicit --system) installs a LaunchDaemon (runs at boot).
//
// For a LaunchDaemon with no explicit --user, it prefers $SUDO_USER and
// falls back to root (with a warning). ``note`` is a possibly-empty,
// possibly-multi-line message explaining an automatic choice.
func ResolveLaunchdMode(isRoot, systemFlag bool, userFlag, sudoUser string) (mode ServiceMode, runUser, note string) {
	switch {
	case systemFlag:
		// Explicit LaunchDaemon; no auto-select note.
		mode = ServiceModeSystem
	case isRoot:
		mode = ServiceModeSystem
		note = "Running as root — installing a LaunchDaemon (runs at boot)."
	default:
		// LaunchAgent runs as the invoking user; runUser is implicit.
		return ServiceModeUser, "", ""
	}

	if userFlag != "" {
		runUser = userFlag
		return mode, runUser, note
	}
	if sudoUser != "" && sudoUser != "root" {
		runUser = sudoUser
		return mode, runUser, note
	}
	runUser = "root"
	warn := "No --user given; the LaunchDaemon will run as root. " +
		"Re-run with --user <name> to run as a less-privileged user."
	if note == "" {
		note = warn
	} else {
		note += "\n" + warn
	}
	return mode, runUser, note
}

// userHomeDir resolves the home directory for the given username. An empty
// name (the LaunchAgent path, which runs as the invoking user) returns the
// current user's home; a named user is resolved via the OS user database.
func userHomeDir(username string) (string, error) {
	if username == "" {
		return os.UserHomeDir()
	}
	u, err := user.Lookup(username)
	if err != nil {
		return "", fmt.Errorf("lookup user %q: %w", username, err)
	}
	return u.HomeDir, nil
}

// LaunchdPlistPath returns the absolute plist path for the given mode.
// LaunchAgent goes under the runtime user's ~/Library/LaunchAgents;
// LaunchDaemon under /Library/LaunchDaemons.
func LaunchdPlistPath(mode ServiceMode, runUser string) (string, error) {
	if mode == ServiceModeSystem {
		return filepath.Join(launchdDaemonDir, LaunchdLabel+".plist"), nil
	}
	home, err := userHomeDir(runUser)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, launchdAgentSub, LaunchdLabel+".plist"), nil
}

// LaunchdLogPath returns the log file path written into the plist. Both modes
// log to the runtime user's ~/.docsgpt/host.log so the file is owned by, and
// readable to, the user the daemon runs as.
func LaunchdLogPath(mode ServiceMode, runUser string) (string, error) {
	home, err := userHomeDir(runUser)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".docsgpt", "host.log"), nil
}

// RenderLaunchdPlist returns the plist XML for the given mode.
//
// ``exec`` is the absolute path to the docsgpt-cli binary; ``logPath`` is the
// StandardOut/StandardError destination. For ServiceModeSystem, ``runUser``
// (when non-empty) is emitted as a UserName key so the daemon does not run as
// root; it is ignored for ServiceModeUser (LaunchAgents run as the user).
//
// KeepAlive is {SuccessfulExit=false}: restart on a non-zero exit (crash),
// but not on a clean exit (a revoke exits 0), so a revoked device does not
// restart-loop.
func RenderLaunchdPlist(mode ServiceMode, exec, runUser, logPath string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" ` +
		`"http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")
	b.WriteString("    <key>Label</key>\n")
	fmt.Fprintf(&b, "    <string>%s</string>\n", LaunchdLabel)
	b.WriteString("    <key>ProgramArguments</key>\n")
	b.WriteString("    <array>\n")
	fmt.Fprintf(&b, "        <string>%s</string>\n", exec)
	b.WriteString("        <string>host</string>\n")
	b.WriteString("    </array>\n")
	// LaunchDaemons may run as a non-root user via UserName.
	if mode == ServiceModeSystem && runUser != "" {
		b.WriteString("    <key>UserName</key>\n")
		fmt.Fprintf(&b, "    <string>%s</string>\n", runUser)
	}
	b.WriteString("    <key>RunAtLoad</key>\n")
	b.WriteString("    <true/>\n")
	b.WriteString("    <key>KeepAlive</key>\n")
	b.WriteString("    <dict>\n")
	b.WriteString("        <key>SuccessfulExit</key>\n")
	b.WriteString("        <false/>\n")
	b.WriteString("    </dict>\n")
	b.WriteString("    <key>StandardOutPath</key>\n")
	fmt.Fprintf(&b, "    <string>%s</string>\n", logPath)
	b.WriteString("    <key>StandardErrorPath</key>\n")
	fmt.Fprintf(&b, "    <string>%s</string>\n", logPath)
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

// WriteLaunchdPlist writes the plist at ``path`` with 0644 permissions,
// creating parent directories (0755 for daemons, 0700 for the per-user agent
// dir under ~). It also ensures the log file's parent directory exists.
func WriteLaunchdPlist(path, content string, mode ServiceMode) error {
	dirMode := os.FileMode(0755)
	if mode == ServiceModeUser {
		dirMode = 0700
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("mkdir plist dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	return nil
}

// runLaunchctl shells out to launchctl, returning combined output on failure.
func runLaunchctl(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %s",
			strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// LaunchdDomainTarget returns the launchctl domain target for the given mode:
// "gui/<uid>" for a LaunchAgent, "system" for a LaunchDaemon.
func LaunchdDomainTarget(mode ServiceMode) string {
	if mode == ServiceModeSystem {
		return "system"
	}
	return fmt.Sprintf("gui/%d", os.Getuid())
}

// LaunchdServiceTarget returns the launchctl service target ("<domain>/<label>")
// used by `launchctl print`.
func LaunchdServiceTarget(mode ServiceMode) string {
	return LaunchdDomainTarget(mode) + "/" + LaunchdLabel
}

// LaunchdBootstrap loads the plist into launchd. It tries the modern
// `launchctl bootstrap <domain> <plist>` first and, if that fails, falls back
// to the legacy `launchctl load -w <plist>`. Returns the error from the
// fallback (or nil) so the caller can surface manual steps when both fail.
func LaunchdBootstrap(mode ServiceMode, plistPath string) error {
	domain := LaunchdDomainTarget(mode)
	if err := runLaunchctl("bootstrap", domain, plistPath); err == nil {
		return nil
	}
	// Legacy fallback.
	return runLaunchctl("load", "-w", plistPath)
}

// LaunchdBootout unloads the plist from launchd, trying the modern
// `launchctl bootout <domain> <plist>` first and falling back to the legacy
// `launchctl unload -w <plist>`.
func LaunchdBootout(mode ServiceMode, plistPath string) error {
	domain := LaunchdDomainTarget(mode)
	if err := runLaunchctl("bootout", domain, plistPath); err == nil {
		return nil
	}
	return runLaunchctl("unload", "-w", plistPath)
}

// LaunchdBootstrapCmd returns the manual bootstrap command string, shown to
// the user when the automatic load fails.
func LaunchdBootstrapCmd(mode ServiceMode, plistPath string) string {
	return fmt.Sprintf("launchctl bootstrap %s %s",
		LaunchdDomainTarget(mode), plistPath)
}

// DetectInstalledLaunchdMode looks for an existing plist and reports whether
// it is a LaunchDaemon (system) or LaunchAgent (user). Returns found=false
// when neither exists. Mirrors DetectInstalledMode for systemd.
func DetectInstalledLaunchdMode() (mode ServiceMode, plistPath string, found bool) {
	if p, err := LaunchdPlistPath(ServiceModeUser, ""); err == nil {
		if _, err := os.Stat(p); err == nil {
			return ServiceModeUser, p, true
		}
	}
	sysPath, _ := LaunchdPlistPath(ServiceModeSystem, "")
	if _, err := os.Stat(sysPath); err == nil {
		return ServiceModeSystem, sysPath, true
	}
	return ServiceModeUser, "", false
}
