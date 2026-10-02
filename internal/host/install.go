package host

import (
	"fmt"
	"os"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/display"
)

// InstallService installs the daemon with the platform's service backend
// (systemd on Linux, launchd on macOS, Task Scheduler on Windows). Zero
// flags let the mode resolver pick system-vs-user from root. Flag-specific
// guards such as "--system requires root" are the caller's.
func InstallService(systemFlag bool, runUserFlag string) error {
	switch {
	case IsLinux():
		return installSystemd(systemFlag, runUserFlag)
	case IsDarwin():
		return installLaunchd(systemFlag, runUserFlag)
	case IsWindows():
		return installWindowsTask()
	default:
		return ErrUnsupportedOS
	}
}

// UninstallService removes whichever service InstallService set up.
func UninstallService() error {
	switch {
	case IsLinux():
		return uninstallSystemd()
	case IsDarwin():
		return uninstallLaunchd()
	case IsWindows():
		return uninstallWindowsTask()
	default:
		return ErrUnsupportedOS
	}
}

// resolvePairedExecutable runs the shared pre-install checks: the device must
// already be paired (a service that fails on first start is bad UX) and the
// CLI binary path is resolved for the unit/plist. A /tmp binary in user mode
// is warned about since it will not survive a reboot.
func resolvePairedExecutable(mode ServiceMode) (string, error) {
	cfg, err := LoadHostConfig()
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("load host.yml: %w", err)
	}
	if cfg.DeviceID == "" || cfg.SessionToken == "" {
		return "", fmt.Errorf("pair the device first with `docsgpt-cli host pair`")
	}
	execPath, err := ResolveExecutable()
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(execPath, "/tmp/") && mode == ServiceModeUser {
		fmt.Println(display.Warn(
			"binary lives under /tmp; install it somewhere persistent before relying on the service",
		))
	}
	return execPath, nil
}

// printServiceModeNote renders the (possibly multi-line, possibly empty)
// note from ResolveServiceMode / ResolveLaunchdMode. The run-as-root
// advisory is a warning; the auto-select explanation is muted info.
func printServiceModeNote(note string) {
	if note == "" {
		return
	}
	for _, line := range strings.Split(note, "\n") {
		if strings.HasPrefix(line, "No --user given") {
			fmt.Println(display.Warn(line))
		} else {
			fmt.Println(display.Muted(line))
		}
	}
}

// installSystemd resolves the install mode, writes the systemd unit, and
// enables it. When systemctl fails it prints the manual steps instead.
func installSystemd(systemFlag bool, runUserFlag string) error {
	mode, runUser, note := ResolveServiceMode(os.Geteuid() == 0, systemFlag, runUserFlag, os.Getenv("SUDO_USER"))
	printServiceModeNote(note)

	execPath, err := resolvePairedExecutable(mode)
	if err != nil {
		return err
	}
	unitPath, err := ServiceUnitPath(mode)
	if err != nil {
		return err
	}
	if err := WriteUnitFile(unitPath, RenderServiceUnit(mode, execPath, runUser), mode); err != nil {
		return err
	}
	fmt.Println(display.Success("Wrote " + unitPath))

	manualSteps := func() {
		fmt.Println(display.Muted("Run manually:"))
		pfx := strings.Join(SystemctlArgs(mode), " ")
		if pfx != "" {
			pfx += " "
		}
		fmt.Printf("  systemctl %sdaemon-reload\n", pfx)
		fmt.Printf("  systemctl %senable --now %s\n", pfx, ServiceUnitName)
	}
	if err := DaemonReload(mode); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("daemon-reload failed: "+err.Error()))
		manualSteps()
		return nil
	}
	if err := EnableNow(mode); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("enable --now failed: "+err.Error()))
		manualSteps()
		return nil
	}
	fmt.Println(display.Success("Service enabled and started."))
	fmt.Println(display.Muted("Inspect logs: journalctl " + strings.Join(JournalArgs(mode), " ") + " -f"))
	if mode == ServiceModeUser {
		fmt.Println(display.Muted(
			"To start on boot without an active login: run `loginctl enable-linger $USER`",
		))
	}
	return nil
}

// installLaunchd resolves the launchd mode (LaunchAgent vs LaunchDaemon),
// writes the plist, and loads it via launchctl. On a load failure it leaves
// the plist in place and prints the manual bootstrap step.
func installLaunchd(systemFlag bool, runUserFlag string) error {
	mode, runUser, note := ResolveLaunchdMode(os.Geteuid() == 0, systemFlag, runUserFlag, os.Getenv("SUDO_USER"))
	printServiceModeNote(note)

	execPath, err := resolvePairedExecutable(mode)
	if err != nil {
		return err
	}
	plistPath, err := LaunchdPlistPath(mode, runUser)
	if err != nil {
		return err
	}
	logPath, err := LaunchdLogPath(mode, runUser)
	if err != nil {
		return err
	}
	if err := WriteLaunchdPlist(plistPath, RenderLaunchdPlist(mode, execPath, runUser, logPath), mode); err != nil {
		return err
	}
	fmt.Println(display.Success("Wrote " + plistPath))

	if err := LaunchdBootstrap(mode, plistPath); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("launchctl load failed: "+err.Error()))
		fmt.Println(display.Muted("The plist was written. Load it manually:"))
		fmt.Printf("  %s\n", LaunchdBootstrapCmd(mode, plistPath))
		return nil
	}
	fmt.Println(display.Success("Service loaded and started."))
	fmt.Println(display.Muted("Logs: " + logPath))
	fmt.Println(display.Muted("Inspect logs: tail -f " + logPath))
	sudo := ""
	if mode == ServiceModeSystem {
		sudo = "sudo "
	}
	fmt.Println(display.Muted("Check status: " + sudo + "launchctl print " + LaunchdServiceTarget(mode)))
	return nil
}

// installWindowsTask writes the Task Scheduler XML, registers the task via
// schtasks, and starts it. On a registration failure it leaves the XML in
// place and prints the manual command, mirroring the systemd and launchd
// fallbacks. Runs as the current user at logon; no elevation needed.
func installWindowsTask() error {
	execPath, err := resolvePairedExecutable(ServiceModeUser)
	if err != nil {
		return err
	}
	runUser, err := CurrentSystemUser()
	if err != nil {
		return err
	}

	xmlPath := WindowsTaskXMLPath()
	if err := WriteWindowsTaskXML(xmlPath, RenderWindowsTaskXML(execPath, runUser)); err != nil {
		return err
	}
	fmt.Println(display.Success("Wrote " + xmlPath))

	if err := RegisterWindowsTask(xmlPath); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("schtasks create failed: "+err.Error()))
		fmt.Println(display.Muted("The task XML was written. Register it manually:"))
		fmt.Printf("  %s\n", WindowsTaskRegisterCmd(xmlPath))
		return nil
	}
	if err := StartWindowsTask(); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("schtasks run failed: "+err.Error()))
		fmt.Println(display.Muted("Start it manually: schtasks /Run /TN " + WindowsTaskName))
		return nil
	}

	cfg, _ := LoadHostConfig()
	fmt.Println(display.Success("Scheduled task installed and started (runs at logon)."))
	fmt.Println(display.Muted("Logs: " + cfg.LogFile))
	fmt.Println(display.Muted("Check status: schtasks /Query /TN " + WindowsTaskName))
	return nil
}

// uninstallSystemd detects the installed unit, disables it, and removes the
// unit file.
func uninstallSystemd() error {
	mode, found := DetectInstalledMode()
	if !found {
		fmt.Println("no docsgpt-cli-host service installed")
		return nil
	}
	if mode == ServiceModeSystem && os.Geteuid() != 0 {
		return fmt.Errorf("system-mode service detected; rerun with sudo")
	}
	if err := DisableNow(mode); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("disable --now failed: "+err.Error()))
	}
	unitPath, err := ServiceUnitPath(mode)
	if err != nil {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", unitPath, err)
	}
	if err := DaemonReload(mode); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("daemon-reload failed: "+err.Error()))
	}
	fmt.Println(display.Success("Service removed (" + mode.String() + " mode)."))
	return nil
}

// uninstallLaunchd detects the installed plist (LaunchAgent vs
// LaunchDaemon), boots it out of launchd, and deletes the plist file.
func uninstallLaunchd() error {
	mode, plistPath, found := DetectInstalledLaunchdMode()
	if !found {
		fmt.Println("no docsgpt-cli-host service installed")
		return nil
	}
	if mode == ServiceModeSystem && os.Geteuid() != 0 {
		return fmt.Errorf("LaunchDaemon detected; rerun with sudo")
	}
	if err := LaunchdBootout(mode, plistPath); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("launchctl unload failed: "+err.Error()))
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", plistPath, err)
	}
	label := "LaunchAgent"
	if mode == ServiceModeSystem {
		label = "LaunchDaemon"
	}
	fmt.Println(display.Success("Service removed (" + label + ")."))
	return nil
}

// uninstallWindowsTask stops and deletes the scheduled task and removes the
// task XML from ~/.docsgpt.
func uninstallWindowsTask() error {
	if !WindowsTaskInstalled() {
		fmt.Println("no docsgpt-cli-host service installed")
		return nil
	}
	// Failing to end is normal when no instance is running.
	_ = EndWindowsTask()
	if err := DeleteWindowsTask(); err != nil {
		return err
	}
	if err := os.Remove(WindowsTaskXMLPath()); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, display.Warn("remove task xml: "+err.Error()))
	}
	fmt.Println(display.Success("Service removed (scheduled task)."))
	return nil
}
