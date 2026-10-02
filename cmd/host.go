package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/host"
	"github.com/arc53/DocsGPT-cli/internal/update"

	"github.com/spf13/cobra"
)

var (
	hostPollOverride  string
	hostServiceMode   bool
	hostServiceSystem bool
	hostServiceUser   string
	hostResetYes      bool
)

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Run docsgpt-cli as a long-lived daemon paired to a DocsGPT account",
	Long: "Run docsgpt-cli as a long-lived daemon paired to a DocsGPT account.\n\n" +
		"Approval mode is set in the DocsGPT UI under Settings -> Tools -> <your device>.",
	Args: subcommandArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runHostDaemon()
	},
}

// runHostDaemon runs the daemon from host.yml. Shared by the bare `host`
// command and the post-pair "start now" menu action.
func runHostDaemon() error {
	mode := update.ModeOff
	if os.Getenv("DOCSGPT_NO_UPDATE_CHECK") == "" && update.IsReleaseVersion(Version) {
		if cfg, err := config.Load(); err == nil {
			mode = cfg.Settings.AutoUpdateMode()
		}
	}
	err := host.RunDaemon(host.DaemonOptions{
		PollInterval: hostPollOverride,
		ServiceMode:  hostServiceMode,
		Version:      Version,
		AutoUpdate:   mode,
	})
	if errors.Is(err, host.ErrRevoked) {
		// Exit 0: a revoke is an intended, graceful shutdown, not a failure.
		// This keeps systemd Restart=on-failure and launchd
		// KeepAlive={SuccessfulExit=false} from restarting the daemon.
		os.Exit(0)
	}
	return err
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
		pr, err := host.Pair(baseURL, code, Version)
		if err != nil {
			return err
		}
		fmt.Println(display.Success("Paired as " + pr.Name + " ✓"))
		fmt.Println(display.Muted("Device ID: " + pr.DeviceID))

		// Non-TTY (piped stdin, e.g. `echo CODE | ... host pair`): print the
		// hint and return. Scripts and SSH pipes depend on this not blocking
		// on menu input.
		if !stdinIsTerminal() {
			fmt.Println(display.Muted("Run `docsgpt-cli host` to start the daemon."))
			return nil
		}
		return runPairMenu()
	},
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
	return append(actions, pairMenuNothing)
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
	if err != nil || n < 1 || n > numOptions {
		return 0, false
	}
	return n - 1, true
}

// runPairMenu renders the interactive post-pair menu and dispatches the
// chosen action. Default (bare Enter) is the last option, "Nothing for
// now". Invalid input re-prompts once, then falls back to the default.
func runPairMenu() error {
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
		fmt.Println(display.Muted("No valid choice; doing nothing."))
		break
	}

	switch actions[choice] {
	case pairMenuStart:
		fmt.Println()
		return runHostDaemon()
	case pairMenuInstall:
		fmt.Println()
		return host.InstallService(false, "")
	default: // pairMenuNothing
		fmt.Println(display.Muted("Run `docsgpt-cli host` to start the daemon."))
		return nil
	}
}

// parseServerTime parses a device timestamp (RFC3339, with or without
// fractional seconds).
func parseServerTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
	}
	return t, err
}

// formatLastSeen produces "online (last seen 14s ago)" / "offline (last
// seen 5m ago)" / "never connected" based on the device row.
func formatLastSeen(d *host.DeviceMe) string {
	if d.LastSeenAt == "" {
		return "never connected"
	}
	t, err := parseServerTime(d.LastSeenAt)
	if err != nil {
		return "last seen " + d.LastSeenAt
	}
	age := time.Since(t)
	tag := "offline"
	if age < 30*time.Second {
		tag = "online"
	}
	return fmt.Sprintf("%s (last seen %s ago)", tag, host.HumanDuration(age))
}

// formatPaired renders the paired_at field. Empty → "unknown".
func formatPaired(d *host.DeviceMe) string {
	if d.PairedAt == "" {
		return "unknown"
	}
	t, err := parseServerTime(d.PairedAt)
	if err != nil {
		return d.PairedAt
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

		d, unauthorized, fetchErr := host.FetchDeviceMe(cfg, 10*time.Second)
		if unauthorized {
			fmt.Fprintln(os.Stderr, display.Danger("device has been revoked on the server"))
			os.Exit(1)
		}
		if d == nil {
			// Server unreachable — fall back to local-only output.
			if fetchErr != nil {
				fmt.Println(display.Warn("(server unreachable - showing local state only)"))
				fmt.Println(display.Muted("reason: " + fetchErr.Error()))
			}
			// approval_mode lives server-side and the local cache is
			// unreliable, so it is only reported from a live fetch.
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
		if !host.HasLocalState() {
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
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if !strings.EqualFold(strings.TrimSpace(line), "y") {
				fmt.Println("aborted")
				return nil
			}
		}
		removed, err := host.ClearLocalState()
		if err != nil {
			return err
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
			// System-wide install dirs and the loader need root.
			if os.Geteuid() != 0 {
				return fmt.Errorf("--system requires root; rerun with sudo")
			}
		}
		return host.InstallService(hostServiceSystem, hostServiceUser)
	},
}

var hostUninstallServiceCmd = &cobra.Command{
	Use:   "uninstall-service",
	Short: "Remove the host daemon service (systemd / launchd / Task Scheduler)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return host.UninstallService()
	},
}

var hostRotateMachineKeyCmd = &cobra.Command{
	Use:    "rotate-machine-key",
	Short:  "Rotate the Ed25519 machine key with a signed handoff (stub)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(display.Warn("rotate-machine-key is not implemented yet."))
		return nil
	},
}

func init() {
	hostCmd.Flags().StringVar(&hostPollOverride, "poll-interval", "", "Override polling interval (e.g. 10s)")
	hostCmd.Flags().BoolVar(&hostServiceMode, "service", false,
		"Run as a background service: append output to the host log file "+
			"instead of the terminal (used by the Windows scheduled task)")

	hostInstallServiceCmd.Flags().BoolVar(&hostServiceSystem, "system", false, "Install as a system-wide service (requires sudo)")
	hostInstallServiceCmd.Flags().StringVar(&hostServiceUser, "user", "", "Runtime user for system services (defaults to $SUDO_USER, else root)")

	hostResetCmd.Flags().BoolVar(&hostResetYes, "yes", false, "Skip the interactive confirmation prompt")

	hostCmd.AddCommand(hostPairCmd, hostStatusCmd, hostRevokeCmd, hostResetCmd,
		hostInstallServiceCmd, hostUninstallServiceCmd, hostRotateMachineKeyCmd)
}
