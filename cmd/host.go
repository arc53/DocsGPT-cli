package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"docsgpt-cli/internal/display"
	"docsgpt-cli/internal/host"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	hostPollOverride  string
	hostServiceMode   bool
	hostServiceSystem bool
	hostServiceUser   string
	hostResetYes      bool
)

// idleHeartbeatInterval is how often the daemon prints an "idle" line
// when no work has arrived for a while. Keeps the user reassured the
// process is alive without spamming on every poll.
const idleHeartbeatInterval = 60 * time.Second

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Run docsgpt-cli as a long-lived daemon paired to a DocsGPT account",
	Long: "Run docsgpt-cli as a long-lived daemon paired to a DocsGPT account.\n\n" +
		"Approval mode is set in the DocsGPT UI under Settings -> Tools -> <your device>.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runHostDaemon(cmd)
	},
}

// runHostDaemon loads host.yml and runs the long-lived poll/stream loop.
// Shared by the bare `host` command and the post-pair "start now" menu
// action; both rely on host.yml already being on disk.
func runHostDaemon(cmd *cobra.Command) error {
	cfg, err := host.LoadHostConfig()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load host.yml: %w", err)
	}
	// Service mode (Windows scheduled task): redirect output to the log
	// file and drop the console window before anything is printed, so even
	// the "not paired" error below lands somewhere inspectable.
	if hostServiceMode {
		if err := host.EnterServiceMode(cfg.LogFile); err != nil {
			return err
		}
	}
	if cfg.DeviceID == "" || cfg.SessionToken == "" {
		return fmt.Errorf("not paired. Run `docsgpt-cli host pair` first")
	}
	if hostPollOverride != "" {
		cfg.PollInterval = hostPollOverride
	}

	key, err := host.LoadOrCreateKey()
	if err != nil {
		return err
	}
	host.ShowStartupBanner(cfg)

	t := host.NewTransport(cfg, key, rootCmd.Version)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		fmt.Println(display.Muted("Shutting down host..."))
		cancel()
	}()

	// Idle heartbeat: while we're in the Polling state with no recent
	// session, print one line per `idleHeartbeatInterval` so the user
	// knows the daemon is alive.
	go runIdleHeartbeat(ctx, t)

	// Each invocation arrives via OnInvocation; spawn a goroutine to
	// execute + stream so the SSE loop keeps reading next events.
	t.OnInvocation = func(inv host.Invocation) {
		go host.ExecuteAndStream(ctx, t, t.Baton.SessionID(), inv)
	}

	fastUntil := time.Time{}
	for {
		if ctx.Err() != nil {
			return nil
		}
		pr, err := t.RunPolling(ctx, fastUntil)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, host.ErrRevoked) {
				fmt.Fprintln(os.Stderr, display.Warn(
					"device has been revoked, terminating",
				))
				// Exit 0: a revoke is an intended, graceful shutdown, not a
				// failure. This keeps systemd Restart=on-failure and launchd
				// KeepAlive={SuccessfulExit=false} from restarting the daemon.
				os.Exit(0)
			}
			return err
		}
		sessionID := pr.SessionTicket
		t.Baton.SetSessionID(sessionID)
		fmt.Println(display.Muted(host.LogStamp(time.Now()) + " session opened"))
		if err := t.RunSSE(ctx, sessionID, ""); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, host.ErrRevoked) {
				fmt.Fprintln(os.Stderr, display.Warn(
					"device has been revoked, terminating",
				))
				// Exit 0: graceful, intended termination (see polling loop).
				os.Exit(0)
			}
			fmt.Fprintln(os.Stderr, display.Warn("SSE error: "+err.Error()))
		}
		t.Baton.Transition(host.StateStreaming, host.StatePolling)
		fmt.Println(display.Muted(host.LogStamp(time.Now()) + " session closed"))
		fastUntil = time.Now().Add(30 * time.Second)
	}
}

// runIdleHeartbeat prints a periodic "idle" line while polling, so a
// service-mode user has positive evidence the daemon is alive. Streaming
// sessions emit their own opened/closed lines, so we suppress the
// heartbeat during streaming.
func runIdleHeartbeat(ctx context.Context, t *host.Transport) {
	ticker := time.NewTicker(idleHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if t.Baton.State() != host.StatePolling {
				continue
			}
			suffix := ""
			if last := t.Baton.LastActivity(); !last.IsZero() {
				suffix = fmt.Sprintf(" (last connected: %s ago)", humanDuration(time.Since(last)))
			}
			fmt.Println(display.Muted(fmt.Sprintf(
				"%s idle - polling every %s%s",
				host.LogStamp(time.Now()),
				t.Cfg.PollInterval,
				suffix,
			)))
		}
	}
}

// humanDuration renders a Duration as 14s, 2m, 1h, etc. Trades precision
// for legibility — these are status lines, not metrics.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

var hostPairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair this machine to a DocsGPT account",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := host.LoadHostConfig()
		fmt.Print("Pairing code (XXXX-XXXX): ")
		var code string
		fmt.Scanln(&code)
		code = strings.TrimSpace(code)
		if code == "" {
			return fmt.Errorf("no pairing code entered")
		}
		baseURL := cfg.BaseURL
		if globalURL != "" {
			baseURL = globalURL
		}
		pr, err := host.Pair(baseURL, code, rootCmd.Version)
		if err != nil {
			return err
		}
		fmt.Println(display.Success("Paired as " + pr.Name + " ✓"))
		fmt.Println(display.Muted("Device ID: " + pr.DeviceID))

		// Non-TTY (piped stdin, e.g. `echo CODE | ... host pair`): keep the
		// original behavior — print the hint and return, no prompt. Scripts
		// and SSH pipes depend on this not blocking on menu input.
		if !stdinIsTTY() {
			fmt.Println(display.Muted("Run `docsgpt-cli host` to start the daemon."))
			return nil
		}
		return runPairMenu(cmd)
	},
}

// stdinIsTTY reports whether stdin is an interactive terminal. The menu is
// only shown when a human is present to answer it.
func stdinIsTTY() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// pairMenuStart / pairMenuInstall are the literal action labels, also used
// to map a chosen index back to behavior (the install option is absent on
// unsupported platforms, so we compare by label rather than fixed index).
const (
	pairMenuStart   = "Start the host daemon now (foreground)"
	pairMenuInstall = "Install as a service (starts automatically)"
	pairMenuNothing = "Nothing for now"
)

// buildPairMenuActions returns the post-pair menu options for the given OS.
// "Install as a service" appears on Linux (systemd), macOS (launchd), and
// Windows (Task Scheduler); "Nothing for now" is always last so it can
// serve as the safe default.
func buildPairMenuActions(goos string) []string {
	actions := []string{pairMenuStart}
	if goos == "linux" || goos == "darwin" || goos == "windows" {
		actions = append(actions, pairMenuInstall)
	}
	actions = append(actions, pairMenuNothing)
	return actions
}

// parsePairMenuChoice maps raw menu input to a zero-based action index.
// Empty input selects defaultIdx. Returns ok=false for non-numeric or
// out-of-range input so the caller can re-prompt.
func parsePairMenuChoice(input string, numOptions, defaultIdx int) (idx int, ok bool) {
	s := strings.TrimSpace(input)
	if s == "" {
		return defaultIdx, true
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	if n < 1 || n > numOptions {
		return 0, false
	}
	return n - 1, true
}

// runPairMenu renders the interactive post-pair menu and dispatches the
// chosen action. Default (bare Enter) is the last option, "Nothing for
// now". Invalid input re-prompts once, then falls back to the default.
func runPairMenu(cmd *cobra.Command) error {
	actions := buildPairMenuActions(runtime.GOOS)
	defaultIdx := len(actions) - 1 // "Nothing for now"

	fmt.Println()
	fmt.Println(display.Accent("What would you like to do?"))
	for i, a := range actions {
		fmt.Printf("  %s %s\n", display.Accent(fmt.Sprintf("%d)", i+1)), a)
	}

	reader := bufio.NewReader(os.Stdin)
	choice := defaultIdx
	for attempt := 0; ; attempt++ {
		fmt.Printf("\nEnter choice [%d]: ", defaultIdx+1)
		line, _ := reader.ReadString('\n')
		idx, ok := parsePairMenuChoice(line, len(actions), defaultIdx)
		if ok {
			choice = idx
			break
		}
		if attempt == 0 {
			fmt.Println(display.Warn("Invalid choice, try again."))
			continue
		}
		// Second invalid input: fall back to the safe default.
		fmt.Println(display.Muted("No valid choice; doing nothing."))
		choice = defaultIdx
		break
	}

	switch actions[choice] {
	case pairMenuStart:
		fmt.Println()
		return runHostDaemon(cmd)
	case pairMenuInstall:
		fmt.Println()
		// Zero flags: let ResolveServiceMode pick system-vs-user from root,
		// exactly like the bare `host install-service`.
		return installHostService(false, "")
	default: // pairMenuNothing
		fmt.Println(display.Muted("Run `docsgpt-cli host` to start the daemon."))
		return nil
	}
}

// statusFetchTimeout bounds the live device lookup for `host status`.
const statusFetchTimeout = 10 * time.Second

// formatLastSeen produces "online (last seen 14s ago)" / "offline (last
// seen 5m ago)" / "never connected" based on the device row.
func formatLastSeen(d *host.DeviceMe) string {
	if d.LastSeenAt == "" {
		return "never connected"
	}
	t, err := time.Parse(time.RFC3339Nano, d.LastSeenAt)
	if err != nil {
		// Fall back to the simpler RFC3339 if Nano parse fails.
		t, err = time.Parse(time.RFC3339, d.LastSeenAt)
		if err != nil {
			return "last seen " + d.LastSeenAt
		}
	}
	age := time.Since(t)
	tag := "offline"
	if age < 30*time.Second {
		tag = "online"
	}
	return fmt.Sprintf("%s (last seen %s ago)", tag, humanDuration(age))
}

// formatPaired renders the paired_at field. Empty → "unknown".
func formatPaired(d *host.DeviceMe) string {
	if d.PairedAt == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339Nano, d.PairedAt)
	if err != nil {
		t, err = time.Parse(time.RFC3339, d.PairedAt)
		if err != nil {
			return d.PairedAt
		}
	}
	return t.UTC().Format("2006-01-02 15:04 MST")
}

var hostStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the host pairing status (hits the server for live state)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := host.LoadHostConfig()
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if cfg.DeviceID == "" {
			fmt.Println("not paired")
			return nil
		}

		d, unauthorized, fetchErr := host.FetchDeviceMe(cfg, statusFetchTimeout)
		if unauthorized {
			fmt.Fprintln(os.Stderr, display.Danger(
				"device has been revoked on the server",
			))
			os.Exit(1)
		}
		if d == nil {
			// Server unreachable — fall back to local-only output.
			if fetchErr != nil {
				fmt.Println(display.Warn(
					"(server unreachable - showing local state only)",
				))
				fmt.Println(display.Muted("reason: " + fetchErr.Error()))
			}
			// approval_mode is intentionally omitted: it lives server-side
			// and the local cache is unreliable. We only report it when the
			// live fetch succeeds (below).
			fmt.Printf("device_id:      %s\n", cfg.DeviceID)
			fmt.Printf("base_url:       %s\n", cfg.BaseURL)
			fmt.Printf("approval_mode:  managed in the DocsGPT UI\n")
			fmt.Printf("poll_interval:  %s\n", cfg.PollInterval)
			return nil
		}

		fmt.Printf("device:         %s (%s)\n", d.Name, d.ID)
		fmt.Printf("host:           %s · %s\n", d.Hostname, d.OS)
		fmt.Printf("status:         %s\n", formatLastSeen(d))
		fmt.Printf("approval_mode:  %s\n", d.ApprovalMode)
		fmt.Printf("base_url:       %s\n", cfg.BaseURL)
		fmt.Printf("poll_interval:  %s\n", cfg.PollInterval)
		fmt.Printf("paired:         %s\n", formatPaired(d))
		if d.Description != "" {
			fmt.Printf("description:    %s\n", d.Description)
		}
		return nil
	},
}

var hostRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke this device on the server and clear local state",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := host.LoadHostConfig()
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if cfg.DeviceID == "" {
			fmt.Println("not paired; nothing to revoke")
			return nil
		}
		if err := host.RevokeFromServer(cfg); err != nil {
			fmt.Fprintln(os.Stderr, display.Warn("server revoke failed: "+err.Error()))
		}
		// Clear local state regardless of server outcome.
		cfg.DeviceID = ""
		cfg.SessionToken = ""
		_ = cfg.Save()
		fmt.Println(display.Success("Local pairing cleared."))
		return nil
	},
}

var hostResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Clear local pairing state without contacting the server",
	Long: "Clear local pairing state without contacting the server.\n\n" +
		"The device may remain active on the server. Use `docsgpt-cli host revoke`\n" +
		"to revoke it on both sides.",
	RunE: func(cmd *cobra.Command, args []string) error {
		hostDir := filepath.Join(os.Getenv("HOME"), ".docsgpt")
		yml := filepath.Join(hostDir, "host.yml")
		key := filepath.Join(hostDir, "host.key")
		_, ymlErr := os.Stat(yml)
		_, keyErr := os.Stat(key)
		if os.IsNotExist(ymlErr) && os.IsNotExist(keyErr) {
			fmt.Println("nothing to reset")
			return nil
		}
		fmt.Println(display.Warn(
			"This will clear local pairing state only. The device may remain " +
				"active on the server. Use `docsgpt-cli host revoke` to revoke " +
				"it on both sides.",
		))
		if !hostResetYes {
			fmt.Print("Continue? (y/N) ")
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			if !strings.EqualFold(strings.TrimSpace(line), "y") {
				fmt.Println("aborted")
				return nil
			}
		}
		removed := []string{}
		if err := os.Remove(yml); err == nil {
			removed = append(removed, "host.yml")
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("remove host.yml: %w", err)
		}
		if err := os.Remove(key); err == nil {
			removed = append(removed, "host.key")
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("remove host.key: %w", err)
		}
		fmt.Println(display.Success(fmt.Sprintf(
			"Local pairing cleared (%s).", strings.Join(removed, ", "),
		)))
		return nil
	},
}

var hostInstallServiceCmd = &cobra.Command{
	Use:   "install-service",
	Short: "Install docsgpt-cli host as a service (systemd / launchd / Task Scheduler)",
	Long: "Install docsgpt-cli host as a background service.\n\n" +
		"Linux (systemd):\n" +
		"  As a normal user, installs a user-service at\n" +
		"  ~/.config/systemd/user/docsgpt-cli-host.service (no sudo).\n" +
		"  As root, defaults to a system service at\n" +
		"  /etc/systemd/system/docsgpt-cli-host.service.\n\n" +
		"macOS (launchd):\n" +
		"  As a normal user, installs a LaunchAgent at\n" +
		"  ~/Library/LaunchAgents/com.arc53.docsgpt-cli-host.plist (no sudo).\n" +
		"  With --system (requires sudo), installs a LaunchDaemon at\n" +
		"  /Library/LaunchDaemons/com.arc53.docsgpt-cli-host.plist.\n\n" +
		"Windows (Task Scheduler):\n" +
		"  Installs a scheduled task named docsgpt-cli-host that starts the\n" +
		"  daemon at logon as the current user and restarts it on failure.\n" +
		"  No administrator rights needed. The daemon logs to\n" +
		"  %USERPROFILE%\\.docsgpt\\host.log.\n\n" +
		"Pass --user <name> to pick the runtime user for a system service;\n" +
		"otherwise it uses $SUDO_USER, falling back to root.\n\n" +
		"Pass --system to force system mode explicitly (requires root;\n" +
		"Linux/macOS only).",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !host.ServiceInstallSupported() {
			return host.ErrUnsupportedOS
		}
		if hostServiceSystem {
			if host.IsWindows() {
				return fmt.Errorf("--system is not supported on Windows; " +
					"the scheduled task always installs for the current user")
			}
			// Explicit --system without root is unworkable on Linux/macOS:
			// system-wide install dirs and the loader need root.
			if os.Geteuid() != 0 {
				return fmt.Errorf("--system requires root; rerun with sudo")
			}
		}
		return installHostService(hostServiceSystem, hostServiceUser)
	},
}

// installHostService dispatches to the platform's service backend (systemd
// on Linux, launchd on macOS, Task Scheduler on Windows). Shared by the
// `host install-service` command (passing its flags) and the post-pair menu
// (passing zero values so the mode resolver picks system-vs-user from root,
// like the bare command). Caller is responsible for flag-specific guards
// such as "--system requires root".
func installHostService(systemFlag bool, runUserFlag string) error {
	switch {
	case host.IsLinux():
		return installSystemdService(systemFlag, runUserFlag)
	case host.IsDarwin():
		return installLaunchdService(systemFlag, runUserFlag)
	case host.IsWindows():
		return installWindowsTaskService()
	default:
		return host.ErrUnsupportedOS
	}
}

// resolvePairedExecutable runs the shared pre-install checks: the device must
// already be paired (a service that fails on first start is bad UX) and the
// CLI binary path is resolved for the unit/plist. A /tmp binary in user mode
// is warned about since it will not survive a reboot.
func resolvePairedExecutable(mode host.ServiceMode) (execPath string, err error) {
	cfg, err := host.LoadHostConfig()
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("load host.yml: %w", err)
	}
	if cfg.DeviceID == "" || cfg.SessionToken == "" {
		return "", fmt.Errorf("pair the device first with `docsgpt-cli host pair`")
	}
	execPath, err = host.ResolveExecutable()
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(execPath, "/tmp/") && mode == host.ServiceModeUser {
		fmt.Println(display.Warn(
			"binary lives under /tmp; install it somewhere persistent before relying on the service",
		))
	}
	return execPath, nil
}

// installSystemdService resolves the install mode, writes the systemd unit,
// and enables it.
func installSystemdService(systemFlag bool, runUserFlag string) error {
	isRoot := os.Geteuid() == 0
	mode, runUser, note := host.ResolveServiceMode(
		isRoot, systemFlag, runUserFlag, os.Getenv("SUDO_USER"),
	)
	printServiceModeNote(note)

	execPath, err := resolvePairedExecutable(mode)
	if err != nil {
		return err
	}

	unitPath, err := host.ServiceUnitPath(mode)
	if err != nil {
		return err
	}
	content := host.RenderServiceUnit(mode, execPath, runUser)
	if err := host.WriteUnitFile(unitPath, content, mode); err != nil {
		return err
	}
	fmt.Println(display.Success("Wrote " + unitPath))

	if err := host.DaemonReload(mode); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("daemon-reload failed: "+err.Error()))
		fmt.Println(display.Muted("Run manually:"))
		printManualSteps(mode)
		return nil
	}
	if err := host.EnableNow(mode); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("enable --now failed: "+err.Error()))
		fmt.Println(display.Muted("Run manually:"))
		printManualSteps(mode)
		return nil
	}
	fmt.Println(display.Success("Service enabled and started."))
	fmt.Println(display.Muted("Inspect logs: journalctl " +
		strings.Join(host.JournalArgs(mode), " ") + " -f"))
	if mode == host.ServiceModeUser {
		fmt.Println(display.Muted(
			"To start on boot without an active login: run `loginctl enable-linger $USER`",
		))
	}
	return nil
}

// installLaunchdService resolves the launchd mode (LaunchAgent vs
// LaunchDaemon), writes the plist, and loads it via launchctl. On a load
// failure it leaves the plist in place and prints the manual bootstrap step.
func installLaunchdService(systemFlag bool, runUserFlag string) error {
	isRoot := os.Geteuid() == 0
	mode, runUser, note := host.ResolveLaunchdMode(
		isRoot, systemFlag, runUserFlag, os.Getenv("SUDO_USER"),
	)
	printServiceModeNote(note)

	execPath, err := resolvePairedExecutable(mode)
	if err != nil {
		return err
	}

	plistPath, err := host.LaunchdPlistPath(mode, runUser)
	if err != nil {
		return err
	}
	logPath, err := host.LaunchdLogPath(mode, runUser)
	if err != nil {
		return err
	}
	content := host.RenderLaunchdPlist(mode, execPath, runUser, logPath)
	if err := host.WriteLaunchdPlist(plistPath, content, mode); err != nil {
		return err
	}
	fmt.Println(display.Success("Wrote " + plistPath))

	if err := host.LaunchdBootstrap(mode, plistPath); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("launchctl load failed: "+err.Error()))
		fmt.Println(display.Muted("The plist was written. Load it manually:"))
		fmt.Printf("  %s\n", host.LaunchdBootstrapCmd(mode, plistPath))
		return nil
	}
	fmt.Println(display.Success("Service loaded and started."))
	fmt.Println(display.Muted("Logs: " + logPath))
	fmt.Println(display.Muted("Inspect logs: tail -f " + logPath))
	if mode == host.ServiceModeSystem {
		fmt.Println(display.Muted("Check status: sudo launchctl print " +
			host.LaunchdServiceTarget(mode)))
	} else {
		fmt.Println(display.Muted("Check status: launchctl print " +
			host.LaunchdServiceTarget(mode)))
	}
	return nil
}

var hostUninstallServiceCmd = &cobra.Command{
	Use:   "uninstall-service",
	Short: "Remove the host daemon service (systemd / launchd / Task Scheduler)",
	RunE: func(cmd *cobra.Command, args []string) error {
		switch {
		case host.IsLinux():
			return uninstallSystemdService()
		case host.IsDarwin():
			return uninstallLaunchdService()
		case host.IsWindows():
			return uninstallWindowsTaskService()
		default:
			return host.ErrUnsupportedOS
		}
	},
}

// installWindowsTaskService writes the Task Scheduler XML, registers the
// task via schtasks, and starts it. On a registration failure it leaves the
// XML in place and prints the manual command, mirroring the systemd and
// launchd fallbacks. Runs as the current user at logon; no elevation needed.
func installWindowsTaskService() error {
	execPath, err := resolvePairedExecutable(host.ServiceModeUser)
	if err != nil {
		return err
	}
	runUser, err := host.CurrentSystemUser()
	if err != nil {
		return err
	}

	xmlPath := host.WindowsTaskXMLPath()
	content := host.RenderWindowsTaskXML(execPath, runUser)
	if err := host.WriteWindowsTaskXML(xmlPath, content); err != nil {
		return err
	}
	fmt.Println(display.Success("Wrote " + xmlPath))

	if err := host.RegisterWindowsTask(xmlPath); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("schtasks create failed: "+err.Error()))
		fmt.Println(display.Muted("The task XML was written. Register it manually:"))
		fmt.Printf("  %s\n", host.WindowsTaskRegisterCmd(xmlPath))
		return nil
	}
	if err := host.StartWindowsTask(); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("schtasks run failed: "+err.Error()))
		fmt.Println(display.Muted("Start it manually: schtasks /Run /TN " + host.WindowsTaskName))
		return nil
	}

	cfg, _ := host.LoadHostConfig()
	fmt.Println(display.Success("Scheduled task installed and started (runs at logon)."))
	fmt.Println(display.Muted("Logs: " + cfg.LogFile))
	fmt.Println(display.Muted("Check status: schtasks /Query /TN " + host.WindowsTaskName))
	return nil
}

// uninstallWindowsTaskService stops and deletes the scheduled task and
// removes the task XML from ~/.docsgpt.
func uninstallWindowsTaskService() error {
	if !host.WindowsTaskInstalled() {
		fmt.Println("no docsgpt-cli-host service installed")
		return nil
	}
	// Failing to end is normal when no instance is running.
	_ = host.EndWindowsTask()
	if err := host.DeleteWindowsTask(); err != nil {
		return err
	}
	if err := os.Remove(host.WindowsTaskXMLPath()); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, display.Warn("remove task xml: "+err.Error()))
	}
	fmt.Println(display.Success("Service removed (scheduled task)."))
	return nil
}

// uninstallSystemdService detects the installed unit, disables it, and removes
// the unit file.
func uninstallSystemdService() error {
	mode, found := host.DetectInstalledMode()
	if !found {
		fmt.Println("no docsgpt-cli-host service installed")
		return nil
	}
	if mode == host.ServiceModeSystem && os.Geteuid() != 0 {
		return fmt.Errorf("system-mode service detected; rerun with sudo")
	}
	if err := host.DisableNow(mode); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("disable --now failed: "+err.Error()))
	}
	unitPath, err := host.ServiceUnitPath(mode)
	if err != nil {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", unitPath, err)
	}
	if err := host.DaemonReload(mode); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("daemon-reload failed: "+err.Error()))
	}
	fmt.Println(display.Success("Service removed (" + mode.String() + " mode)."))
	return nil
}

// uninstallLaunchdService detects the installed plist (LaunchAgent vs
// LaunchDaemon), boots it out of launchd, and deletes the plist file.
func uninstallLaunchdService() error {
	mode, plistPath, found := host.DetectInstalledLaunchdMode()
	if !found {
		fmt.Println("no docsgpt-cli-host service installed")
		return nil
	}
	if mode == host.ServiceModeSystem && os.Geteuid() != 0 {
		return fmt.Errorf("LaunchDaemon detected; rerun with sudo")
	}
	if err := host.LaunchdBootout(mode, plistPath); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("launchctl unload failed: "+err.Error()))
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", plistPath, err)
	}
	label := "LaunchAgent"
	if mode == host.ServiceModeSystem {
		label = "LaunchDaemon"
	}
	fmt.Println(display.Success("Service removed (" + label + ")."))
	return nil
}

var hostRotateMachineKeyCmd = &cobra.Command{
	Use:   "rotate-machine-key",
	Short: "Rotate the Ed25519 machine key with a signed handoff (stub)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(display.Warn("rotate-machine-key is not implemented yet."))
		return nil
	},
}

// printServiceModeNote renders the (possibly multi-line, possibly empty)
// note from host.ResolveServiceMode. The run-as-root advisory is a
// warning; the auto-select explanation is muted info.
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

// printManualSteps prints the systemctl commands the user should run by
// hand when the auto-install path fails (e.g. enabling lingering, missing
// systemd in the session).
func printManualSteps(mode host.ServiceMode) {
	pfx := strings.Join(host.SystemctlArgs(mode), " ")
	if pfx != "" {
		pfx += " "
	}
	fmt.Printf("  systemctl %sdaemon-reload\n", pfx)
	fmt.Printf("  systemctl %senable --now %s\n", pfx, host.ServiceUnitName)
}

func init() {
	hostCmd.Flags().StringVar(&hostPollOverride, "poll-interval", "", "Override polling interval (e.g. 10s)")
	hostCmd.Flags().BoolVar(&hostServiceMode, "service", false,
		"Run as a background service: append output to the host log file "+
			"instead of the terminal (used by the Windows scheduled task)")

	hostInstallServiceCmd.Flags().BoolVar(&hostServiceSystem, "system", false, "Install as a system-wide service (requires sudo)")
	hostInstallServiceCmd.Flags().StringVar(&hostServiceUser, "user", "", "Runtime user for system services (defaults to $SUDO_USER, else root)")

	hostResetCmd.Flags().BoolVar(&hostResetYes, "yes", false, "Skip the interactive confirmation prompt")

	hostCmd.AddCommand(hostPairCmd)
	hostCmd.AddCommand(hostStatusCmd)
	hostCmd.AddCommand(hostRevokeCmd)
	hostCmd.AddCommand(hostResetCmd)
	hostCmd.AddCommand(hostInstallServiceCmd)
	hostCmd.AddCommand(hostUninstallServiceCmd)
	hostCmd.AddCommand(hostRotateMachineKeyCmd)

	rootCmd.AddCommand(hostCmd)
}
