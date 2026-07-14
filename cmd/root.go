package cmd

import (
	"fmt"
	"os"
	"time"

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

	var updateNotice <-chan string
	if shouldCheckForUpdates() {
		updateNotice = update.BackgroundCheck(Version)
	}

	err := rootCmd.Execute()
	printUpdateNotice(updateNotice)

	if err != nil {
		display.ErrorMsg(err.Error())
		os.Exit(1)
	}
}

func shouldCheckForUpdates() bool {
	if os.Getenv("DOCSGPT_NO_UPDATE_CHECK") != "" {
		return false
	}
	if !isatty.IsTerminal(os.Stderr.Fd()) {
		return false
	}
	if cmd, _, err := rootCmd.Find(os.Args[1:]); err == nil && cmd == updateCmd {
		return false
	}
	if cfg, err := config.Load(); err == nil && cfg.Settings.DisableUpdateCheck {
		return false
	}
	return true
}

// printUpdateNotice waits only briefly for the background check so fast
// commands are not held up; a missed result is picked up on a later run.
func printUpdateNotice(ch <-chan string) {
	if ch == nil {
		return
	}
	select {
	case latest := <-ch:
		if latest != "" {
			fmt.Fprintln(os.Stderr, display.Muted(fmt.Sprintf(
				"\nA new version is available: %s → %s. Run 'docsgpt-cli update'.", Version, latest)))
		}
	case <-time.After(200 * time.Millisecond):
	}
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
