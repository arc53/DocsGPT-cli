package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

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
// github.com/arc53/DocsGPT-cli/cmd/docsgpt-cli@vX.Y.Z`, which the Go toolchain
// records in the build info but cannot stamp with ldflags. Without it such a
// build reports "dev", which IsReleaseVersion rejects — so it would never check
// for updates and `docsgpt-cli update` would refuse to run.
func resolveVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return versionFromBuildInfo(info)
}

// versionFromBuildInfo returns the release version a binary was installed at,
// or "" when it was not installed from the module cache at a release version.
//
// Go also stamps Main.Version from VCS tags, so `go build` on a clean checkout
// of v1.5.1 reports v1.5.1 and would otherwise be indistinguishable from a
// released binary — auto-update would then replace a developer's local build
// with the latest release behind their back. A module-cache build carries a
// checksum in Main.Sum; one built from a working tree never does, and carries
// vcs.* build settings instead.
func versionFromBuildInfo(info *debug.BuildInfo) string {
	if info.Main.Sum == "" {
		return ""
	}
	for _, setting := range info.Settings {
		if strings.HasPrefix(setting.Key, "vcs") {
			return ""
		}
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
		// Theme: flag > config > auto (which asks the terminal).
		theme := globalTheme
		if cfg, err := config.Load(); theme == "" && err == nil {
			theme = cfg.Settings.Theme
		}
		display.InitTheme(theme)
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
		display.ErrorMsg(err.Error())
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

	// Runtime errors of the chat commands print no usage; flag errors exit 2
	// with a pointer to --help.
	for _, c := range []*cobra.Command{askCmd, chatCmd, keysCmd} {
		c.SilenceUsage = true
		c.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
			return usageErrf("%w\nRun '%s --help' for usage.", err, c.CommandPath())
		})
	}

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
