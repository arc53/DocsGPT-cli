package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/update"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Version is stamped at link time (see the Makefile and .goreleaser.yaml).
// It stays "dev" for a plain `go build`, and resolveVersion fills it in for a
// `go install` build, which carries no ldflags.
var Version = "dev"

// resolveVersion recovers the version of a binary built by `go install
// github.com/arc53/DocsGPT-cli@vX.Y.Z`, which the Go toolchain records in the
// build info but cannot stamp with ldflags. Without it such a build reports
// "dev", which IsReleaseVersion rejects — so it would never check for updates
// and `docsgpt-cli update` would refuse to run.
//
// Returns "" when there is nothing better than what we already have: a plain
// `go build` reports the main module version as "(devel)".
func resolveVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if !update.IsReleaseVersion(info.Main.Version) {
		return ""
	}
	return info.Main.Version
}

var (
	globalURL         string
	globalKey         string
	globalToken       string
	globalNoStream    bool
	globalNoContext   bool
	globalAutoApprove bool
	globalTimeout     int
	globalTheme       string
	globalNoMotion    bool
)

var rootCmd = &cobra.Command{
	Use:     "docsgpt-cli",
	Version: Version,
	Short:   "A CLI for interacting with DocsGPT",
	Long:    "Docsgpt-cli is a command-line interface (CLI) tool that allows you to interact with DocsGPT.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := config.MigrateIfNeeded(); err != nil {
			return err
		}

		// Determine theme: flag > config > auto
		theme := globalTheme
		if theme == "" {
			cfg, err := config.Load()
			if err == nil && cfg.Settings.Theme != "" {
				theme = cfg.Settings.Theme
			}
		}
		display.InitTheme(theme)

		// Show startup banner (suppressed for `bench --json` so stdout stays
		// a clean, parseable JSON document, and for the account-level
		// commands, whose stdout is data).
		if !suppressBannerForJSON(cmd) && !hasNoBanner(cmd) {
			cfg, loadErr := config.Load()
			bannerSetting := "always"
			if loadErr == nil && cfg.Settings.Banner != "" {
				bannerSetting = cfg.Settings.Banner
			}
			display.ShowBanner(bannerSetting, globalNoMotion)
		}

		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}
	},
	SilenceErrors: true,
}

func Execute() {
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// Initialize a default theme eagerly so display functions are safe to call
	// even when cobra's arg validation fails before PersistentPreRunE runs.
	display.InitTheme("auto")

	mode, exePath := updateGate()
	if mode == update.ModeOn {
		if v, err := update.ApplyStaged(Version, exePath); err == nil && v != "" {
			fmt.Fprintln(os.Stderr, display.Muted(
				"docsgpt-cli updated to "+v+" (takes effect on your next command)"))
		}
	}
	if mode != "" && update.ShouldSpawnWorker(Version, mode) {
		update.SpawnWorker()
	}

	err := rootCmd.Execute()

	if mode == update.ModeNotify {
		if latest := update.CachedNotice(Version); latest != "" {
			fmt.Fprintln(os.Stderr, display.Muted(fmt.Sprintf(
				"\nA new version is available: %s → %s. Run 'docsgpt-cli update'.", Version, latest)))
		}
	}

	if err != nil {
		// Account-level commands keep stdout for data (tables, JSON, YAML),
		// so their errors go to stderr.
		if c, _, findErr := rootCmd.Find(os.Args[1:]); findErr == nil && hasNoBanner(c) {
			fmt.Fprintln(os.Stderr, display.Danger("Error:"), err.Error())
		} else {
			display.ErrorMsg(err.Error())
		}
		os.Exit(exitCodeFor(err))
	}
}

// updateGate decides how the passive update machinery behaves for this
// invocation. "" disables it entirely: opted out, no TTY, a non-release
// build, or the update/host commands (which run their own update logic).
// Installs we cannot swap (Homebrew, unwritable dir) downgrade on → notify.
func updateGate() (mode string, exePath string) {
	if os.Getenv("DOCSGPT_NO_UPDATE_CHECK") != "" {
		return "", ""
	}
	if !isatty.IsTerminal(os.Stderr.Fd()) {
		return "", ""
	}
	if cmd, _, err := rootCmd.Find(os.Args[1:]); err == nil && (cmd == updateCmd || cmd == hostCmd) {
		return "", ""
	}
	if !update.IsReleaseVersion(Version) {
		return "", ""
	}
	cfg, err := config.Load()
	if err != nil {
		return "", ""
	}
	mode = cfg.Settings.AutoUpdateMode()
	if mode == update.ModeOff {
		return "", ""
	}

	exe, err := os.Executable()
	if err != nil {
		return "", ""
	}
	exePath, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", ""
	}
	if mode == update.ModeOn && (update.IsHomebrewPath(exePath) || !isWritable(filepath.Dir(exePath))) {
		mode = update.ModeNotify
	}
	return mode, exePath
}

func init() {
	// Runs after the package-level vars, so rootCmd.Version has already been
	// copied from the unresolved Version and has to be refreshed here too.
	if Version == "dev" {
		if v := resolveVersion(); v != "" {
			Version = v
		}
	}
	rootCmd.Version = Version

	rootCmd.PersistentFlags().StringVar(&globalURL, "url", "", "Override API base URL")
	rootCmd.PersistentFlags().StringVar(&globalKey, "key", "", "Use a specific API key by name")
	rootCmd.PersistentFlags().StringVar(&globalToken, "token", "", "Personal access token (dgpt_pat_…); overrides DOCSGPT_TOKEN and the stored token")
	rootCmd.PersistentFlags().BoolVar(&globalNoStream, "no-stream", false, "Disable streaming")
	rootCmd.PersistentFlags().BoolVar(&globalNoContext, "no-context", false, "Disable context enrichment")
	rootCmd.PersistentFlags().BoolVar(&globalAutoApprove, "auto-approve", false, "Auto-approve tool calls")
	rootCmd.PersistentFlags().IntVar(&globalTimeout, "timeout", 30, "Command execution timeout in seconds")
	rootCmd.PersistentFlags().StringVar(&globalTheme, "theme", "", "Color theme: auto, dark, light")
	rootCmd.PersistentFlags().BoolVar(&globalNoMotion, "no-motion", false, "Disable banner animation")

	rootCmd.AddCommand(askCmd)
	rootCmd.AddCommand(keysCmd)
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(benchCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)
	rootCmd.AddCommand(agentsCmd)
	rootCmd.AddCommand(sourcesCmd)
	rootCmd.AddCommand(promptsCmd)
	rootCmd.AddCommand(toolsCmd)
}

// suppressBannerForJSON reports whether the stdout banner must be skipped: it is
// a bench command invoked with --json, whose stdout must carry only the JSON
// result document.
func suppressBannerForJSON(cmd *cobra.Command) bool {
	isBench := false
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == "bench" {
			isBench = true
			break
		}
	}
	if !isBench {
		return false
	}
	f := cmd.Flags().Lookup("json")
	return f != nil && f.Changed
}
