package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"docsgpt-cli/internal/config"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage DocsGPT CLI configuration",
	Long:  "View and modify CLI configuration such as the API base URL.",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Mask key values for display
		masked := struct {
			BaseURL    string            `json:"base_url"`
			DefaultKey string            `json:"default_key"`
			Keys       map[string]string `json:"keys"`
			Settings   config.Settings   `json:"settings"`
		}{
			BaseURL:    cfg.BaseURL,
			DefaultKey: cfg.DefaultKey,
			Keys:       make(map[string]string),
			Settings:   cfg.Settings,
		}
		for name, key := range cfg.Keys {
			if len(key) > 8 {
				masked.Keys[name] = key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
			} else {
				masked.Keys[name] = "****"
			}
		}

		data, _ := json.MarshalIndent(masked, "", "  ")
		fmt.Println(string(data))
		return nil
	},
}

var configSetURLCmd = &cobra.Command{
	Use:   "set-url [url]",
	Short: "Set the API base URL",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cfg.BaseURL = args[0]
		if err := cfg.Save(); err != nil {
			return err
		}
		green := color.New(color.FgGreen).SprintFunc()
		fmt.Println(green("Base URL set to:"), args[0])
		return nil
	},
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetURLCmd)
}
