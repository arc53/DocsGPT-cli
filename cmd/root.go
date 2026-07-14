package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"docsgpt-cli/internal/config"
	"docsgpt-cli/internal/display"
	"docsgpt-cli/internal/update"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var Version = "dev"

var (
	globalURL         string
	globalKey         string
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

		// Show startup banner
		cfg, loadErr := config.Load()
		bannerSetting := "always"
		if loadErr == nil && cfg.Settings.Banner != "" {
			bannerSetting = cfg.Settings.Banner
		}
		display.ShowBanner(bannerSetting, globalNoMotion)

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
		display.ErrorMsg(err.Error())
		os.Exit(1)
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
	rootCmd.PersistentFlags().StringVar(&globalURL, "url", "", "Override API base URL")
	rootCmd.PersistentFlags().StringVar(&globalKey, "key", "", "Use a specific API key by name")
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
}
