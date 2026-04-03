package cmd

import (
	"os"

	"docsgpt-cli/internal/config"

	"github.com/spf13/cobra"
)

var (
	globalURL         string
	globalKey         string
	globalNoStream    bool
	globalNoContext   bool
	globalAutoApprove bool
	globalTimeout     int
)

var rootCmd = &cobra.Command{
	Use:     "docsgpt-cli",
	Version: "1.0.0",
	Short:   "A CLI for interacting with DocsGPT",
	Long:    "Docsgpt-cli is a command-line interface (CLI) tool that allows you to interact with DocsGPT.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return config.MigrateIfNeeded()
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

	if err := rootCmd.Execute(); err != nil {
		printError(err.Error())
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

	rootCmd.AddCommand(askCmd)
	rootCmd.AddCommand(keysCmd)
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(chatCmd)
}
