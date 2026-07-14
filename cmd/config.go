package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"docsgpt-cli/internal/config"
	"docsgpt-cli/internal/display"
	"docsgpt-cli/internal/update"

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
		fmt.Println(display.Success("Base URL set to:"), args[0])
		return nil
	},
}

var configSetThemeCmd = &cobra.Command{
	Use:   "set-theme [auto|dark|light]",
	Short: "Set the color theme",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		theme := strings.ToLower(args[0])
		if theme != "auto" && theme != "dark" && theme != "light" {
			return fmt.Errorf("invalid theme: %s (use auto, dark, or light)", args[0])
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cfg.Settings.Theme = theme
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println(display.Success("Theme set to:"), theme)
		return nil
	},
}

var configSetBannerCmd = &cobra.Command{
	Use:   "set-banner [always|once|never]",
	Short: "Set the startup banner display mode",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		banner := strings.ToLower(args[0])
		if banner != "always" && banner != "once" && banner != "never" {
			return fmt.Errorf("invalid banner setting: %s (use always, once, or never)", args[0])
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cfg.Settings.Banner = banner
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println(display.Success("Banner set to:"), banner)
		return nil
	},
}

var configSetAutoUpdateCmd = &cobra.Command{
	Use:   "set-auto-update [on|notify|off]",
	Short: "Control automatic updates: on (install, default), notify (notice only), off",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		val := strings.ToLower(args[0])
		if val != "on" && val != "notify" && val != "off" {
			return fmt.Errorf("invalid value: %s (use on, notify, or off)", args[0])
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cfg.Settings.AutoUpdate = val
		cfg.Settings.DisableUpdateCheck = false
		if err := cfg.Save(); err != nil {
			return err
		}
		if val != "on" {
			update.ClearStaging()
		}
		fmt.Println(display.Success("Auto-update set to:"), val)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetURLCmd)
	configCmd.AddCommand(configSetThemeCmd)
	configCmd.AddCommand(configSetBannerCmd)
	configCmd.AddCommand(configSetAutoUpdateCmd)
}
