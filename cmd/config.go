package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	"github.com/arc53/DocsGPT-cli/internal/update"

	"github.com/spf13/cobra"
)

// setting is one entry of `config get/set` and the settings menu.
type setting struct {
	key    string
	title  string   // menu label; "" keeps it out of the menu
	doc    string   // what `config --help` says about the values
	values []string // allowed values, which the menu cycles through
	get    func(*config.Config) string
	set    func(*config.Config, string) error
}

var settings = []setting{
	{
		key: "url", title: "Server", doc: "DocsGPT server URL",
		get: func(c *config.Config) string {
			if c.BaseURL == "" {
				return config.DefaultBaseURL
			}
			return c.BaseURL
		},
		set: func(c *config.Config, v string) error {
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("%q is not an http(s) URL", v)
			}
			c.BaseURL = strings.TrimRight(v, "/")
			return nil
		},
	},
	{
		key: "default_key", doc: "name of the stored API key to chat with",
		get: func(c *config.Config) string { return c.DefaultKey },
		set: func(c *config.Config, v string) error {
			if _, ok := c.Keys[v]; !ok {
				return fmt.Errorf("no stored key named %q (stored: %s)", v, textOrDash(strings.Join(sortedNames(c.Keys), ", ")))
			}
			c.DefaultKey = v
			return nil
		},
	},
	{
		key: "auto_update", title: "Auto-update", values: []string{"on", "notify", "off"},
		get: func(c *config.Config) string { return c.Settings.AutoUpdateMode() },
		set: func(c *config.Config, v string) error {
			c.Settings.AutoUpdate, c.Settings.DisableUpdateCheck = v, false
			return nil
		},
	},
	choice("banner", "Chat banner", func(c *config.Config) *string { return &c.Settings.Banner }, "once", "always", "once", "never"),
	choice("theme", "Theme", func(c *config.Config) *string { return &c.Settings.Theme }, "auto", "auto", "dark", "light"),
	toggle("send_current_directory", "Send working directory", func(c *config.Config) *bool { return &c.Settings.SendCurrentDirectory }),
	toggle("send_directory_contents", "Send directory listing", func(c *config.Config) *bool { return &c.Settings.SendDirectoryContents }),
	toggle("send_last_commands", "Send recent shell commands", func(c *config.Config) *bool { return &c.Settings.SendLastCommands }),
	{
		key: "number_of_last_commands", title: "Shell commands to send", doc: "0-50",
		get: func(c *config.Config) string { return strconv.Itoa(c.Settings.NumberOfLastCommands) },
		set: func(c *config.Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 50 {
				return fmt.Errorf("%q is not a number from 0 to 50", v)
			}
			c.Settings.NumberOfLastCommands = n
			return nil
		},
	},
}

func choice(key, title string, field func(*config.Config) *string, def string, values ...string) setting {
	return setting{
		key: key, title: title, values: values,
		get: func(c *config.Config) string {
			if v := *field(c); v != "" {
				return v
			}
			return def
		},
		set: func(c *config.Config, v string) error {
			*field(c) = v
			return nil
		},
	}
}

func toggle(key, title string, field func(*config.Config) *bool) setting {
	return setting{
		key: key, title: title, values: []string{"true", "false"},
		get: func(c *config.Config) string { return strconv.FormatBool(*field(c)) },
		set: func(c *config.Config, v string) error {
			*field(c) = v == "true"
			return nil
		},
	}
}

func lookupSetting(key string) (setting, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "base_url" {
		key = "url"
	}
	for _, s := range settings {
		if s.key == key {
			return s, nil
		}
	}
	keys := make([]string, len(settings))
	for i, s := range settings {
		keys[i] = s.key
	}
	return setting{}, usageErrf("unknown setting %q (one of: %s)", key, strings.Join(keys, ", "))
}

// applySetting validates and stores value, and saves the config.
func applySetting(cfg *config.Config, s setting, value string) error {
	value = strings.TrimSpace(value)
	if s.values != nil {
		value = strings.ToLower(value)
		switch value {
		case "on", "yes", "1":
			if !slices.Contains(s.values, value) {
				value = "true"
			}
		case "off", "no", "0":
			if !slices.Contains(s.values, value) {
				value = "false"
			}
		}
		if !slices.Contains(s.values, value) {
			return usageErrf("%s must be one of: %s", s.key, strings.Join(s.values, ", "))
		}
	}
	if err := s.set(cfg, value); err != nil {
		return usageErr(err)
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	if s.key == "auto_update" && value != update.ModeOn {
		update.ClearStaging()
	}
	return nil
}

func configHelp() string {
	var b strings.Builder
	b.WriteString(`On a terminal, config opens a menu of the settings below. They can also be
read and changed directly with 'config get <key>' and 'config set <key> <value>':

`)
	for _, s := range settings {
		doc := s.doc
		if doc == "" {
			doc = strings.Join(s.values, " | ")
		}
		fmt.Fprintf(&b, "  %-25s %s\n", s.key, doc)
	}
	b.WriteString("\nDOCSGPT_URL, DOCSGPT_API_KEY and DOCSGPT_TOKEN override the stored values.")
	return b.String()
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show and change settings",
	Long:  configHelp(),
	Example: `  docsgpt-cli config
  docsgpt-cli config set url http://localhost:7091
  docsgpt-cli config get auto_update`,
	Args: subcommandArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !ui.Interactive() {
			return cmd.Help()
		}
		return runConfigMenu(os.Stdout)
	},
}

var configShowJSON bool

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the settings and stored credentials (redacted)",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		return runConfigShow(cfg, configShowJSON, os.Stdout)
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print one setting",
	Args:  usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := lookupSetting(args[0])
		if err != nil {
			return err
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		fmt.Println(s.get(&cfg))
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Change one setting",
	Args:  usageArgs(cobra.ExactArgs(2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setConfig(args[0], args[1])
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file's path",
	Args:  usageArgs(cobra.NoArgs),
	Run:   func(cmd *cobra.Command, args []string) { fmt.Println(config.Path()) },
}

func setConfig(key, value string) error {
	s, err := lookupSetting(key)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := applySetting(&cfg, s, value); err != nil {
		return err
	}
	fmt.Println(display.Success("✓ "+s.key+" =") + " " + s.get(&cfg))
	return nil
}

func runConfigShow(cfg config.Config, asJSON bool, out io.Writer) error {
	keys := map[string]string{}
	for name, key := range cfg.Keys {
		keys[name] = config.RedactKey(key)
	}
	token := ""
	if cfg.Token != "" {
		token = config.RedactToken(cfg.Token)
	}
	if asJSON {
		return writeJSON(out, struct {
			BaseURL    string            `json:"base_url"`
			DefaultKey string            `json:"default_key"`
			Keys       map[string]string `json:"keys"`
			Token      string            `json:"token"`
			Settings   config.Settings   `json:"settings"`
		}{cfg.BaseURL, cfg.DefaultKey, keys, token, cfg.Settings})
	}

	row := func(k, v string) { fmt.Fprintf(out, "  %-25s %s\n", k, v) }
	fmt.Fprintln(out, "Settings "+display.Muted(config.Path()))
	for _, s := range settings {
		row(s.key, textOrDash(s.get(&cfg)))
	}
	fmt.Fprintln(out, "\nAPI keys")
	if len(keys) == 0 {
		row("none", display.Muted("add one with 'docsgpt-cli login'"))
	}
	for _, name := range sortedNames(cfg.Keys) {
		label := name
		if name == cfg.DefaultKey {
			label += " (default)"
		}
		row(label, keys[name])
	}
	fmt.Fprintln(out)
	if token == "" {
		token = "none"
	}
	fmt.Fprintf(out, "%-27s %s\n", "Access token", token)
	var env []string
	for _, name := range []string{config.EnvURL, config.EnvAPIKey, config.EnvToken} {
		if os.Getenv(name) != "" {
			env = append(env, name)
		}
	}
	if len(env) > 0 {
		fmt.Fprintln(out, display.Muted("\nSet in the environment, overriding the above: "+strings.Join(env, ", ")))
	}
	return nil
}

// runConfigMenu shows the settings with their values until Esc: choosing one
// cycles a fixed set of values or asks for a new one.
func runConfigMenu(out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var menu []setting
	for _, s := range settings {
		if s.title != "" {
			menu = append(menu, s)
		}
	}
	cursor, saved := 0, false
	for {
		items := make([]ui.Item, len(menu))
		for i, s := range menu {
			items[i] = ui.Item{Label: s.title, Description: s.get(&cfg), Value: s.key}
		}
		key, err := ui.Select{Title: "Settings", Items: items, Default: cursor, Summary: func(ui.Item) string { return "" }}.Run()
		if errors.Is(err, ui.ErrCancelled) {
			break
		}
		if err != nil {
			return err
		}
		cursor = slices.IndexFunc(menu, func(s setting) bool { return s.key == key })
		s := menu[cursor]
		var value string
		if s.values != nil {
			value = s.values[(slices.Index(s.values, s.get(&cfg))+1)%len(s.values)]
		} else {
			value, err = ui.Input{
				Title: s.title,
				Value: s.get(&cfg),
				Validate: func(_ context.Context, v string) error {
					probe := cfg
					return s.set(&probe, strings.TrimSpace(v))
				},
				Summary: func(string) string { return "" },
			}.Run()
			if errors.Is(err, ui.ErrCancelled) {
				continue
			}
			if err != nil {
				return err
			}
		}
		if err := applySetting(&cfg, s, value); err != nil {
			return err
		}
		saved = true
	}
	if saved {
		fmt.Fprintln(out, display.Muted("Saved to "+config.Path()))
	}
	return nil
}

func init() {
	configShowCmd.Flags().BoolVar(&configShowJSON, "json", false, "Print as JSON")
	configCmd.AddCommand(configShowCmd, configGetCmd, configSetCmd, configPathCmd)
	// The old per-setting commands.
	for name, key := range map[string]string{
		"set-url": "url", "set-theme": "theme", "set-banner": "banner", "set-auto-update": "auto_update",
	} {
		key := key
		configCmd.AddCommand(&cobra.Command{
			Use:    name + " <value>",
			Hidden: true,
			Args:   usageArgs(cobra.ExactArgs(1)),
			RunE:   func(cmd *cobra.Command, args []string) error { return setConfig(key, args[0]) },
		})
	}
}
