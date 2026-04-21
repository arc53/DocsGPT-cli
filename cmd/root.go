package cmd

import (
	"os"

	"docsgpt-cli/internal/config"
	"docsgpt-cli/internal/display"

	"github.com/spf13/cobra"
)

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
	Version: "1.0.0",
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

	if err := rootCmd.Execute(); err != nil {
		display.ErrorMsg(err.Error())
		os.Exit(1)
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
}
