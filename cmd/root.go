package cmd

import (
    "os"
    "github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
    Use:   "docsgpt-cli",
    Version: "0.1.0",
    Short: "A CLI for interacting with DocsGPT",
    Long: "Docsgpt-cli is a command-line interface (CLI) tool that allows you to interact with DocsGPT.",
    Run: func(cmd *cobra.Command, args []string) {
        // Display help if no arguments are provided
        if len(args) == 0 {
            cmd.Help()
            return
        }
    },
    SilenceErrors: true,
}

func Execute() {
    // Disable the completion command by not including it in the rootCmd
    rootCmd.CompletionOptions.DisableDefaultCmd = true

    if err := rootCmd.Execute(); err != nil {
        printError(err.Error())
        os.Exit(1)
    }
}

func init() {
    rootCmd.AddCommand(askCmd)
    rootCmd.AddCommand(keysCmd)
    rootCmd.AddCommand(installCmd)
}
