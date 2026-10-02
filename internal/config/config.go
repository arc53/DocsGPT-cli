package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DefaultBaseURL = "https://gptcloud.arc53.com"

// Environment variables consulted by the resolvers below. They let CI jobs
// run the CLI without a config file.
const (
	EnvAPIKey     = "DOCSGPT_API_KEY"     // agent API key, over the stored default
	EnvToken      = "DOCSGPT_TOKEN"       // personal access token (dgpt_pat_…)
	EnvURL        = "DOCSGPT_URL"         // API base URL
	EnvWebhookURL = "DOCSGPT_WEBHOOK_URL" // agent incoming webhook URL (agents trigger)
)

// TokenPrefix is the fixed prefix of a DocsGPT personal access token.
const TokenPrefix = "dgpt_pat_"

// Token sources reported by ResolveToken.
const (
	TokenSourceFlag   = "--token flag"
	TokenSourceEnv    = EnvToken + " environment variable"
	TokenSourceConfig = "config file"
)

type Config struct {
	BaseURL    string            `json:"base_url"`
	DefaultKey string            `json:"default_key"`
	Keys       map[string]string `json:"keys"`
	// Token is a personal access token (dgpt_pat_…) used by the account-level
	// commands (whoami, agents, sources, prompts, tools) and bench agent_id
	// runs. Stored by `login`, removed by `logout`.
	Token    string   `json:"token,omitempty"`
	Settings Settings `json:"settings"`
}

type Settings struct {
	SendCurrentDirectory  bool `json:"send_current_directory"`
	SendDirectoryContents bool `json:"send_directory_contents"`
	SendLastCommands      bool `json:"send_last_commands"`
	// SendProjectInstructions sends AGENTS.md (or CLAUDE.md) files; a
	// config written before it existed gets the default.
	SendProjectInstructions bool   `json:"send_project_instructions"`
	NumberOfLastCommands    int    `json:"number_of_last_commands"`
	Theme                   string `json:"theme,omitempty"`                // "auto", "dark", "light"
	Banner                  string `json:"banner,omitempty"`               // "always", "once", "never"
	AutoUpdate              string `json:"auto_update,omitempty"`          // "on", "notify", "off"
	DisableUpdateCheck      bool   `json:"disable_update_check,omitempty"` // legacy, superseded by auto_update
}

// AutoUpdateMode resolves the effective auto-update mode: "on" (stage and
// apply updates automatically, the default), "notify" (print a notice only),
// or "off". The legacy disable_update_check flag maps to "off".
func (s Settings) AutoUpdateMode() string {
	switch s.AutoUpdate {
	case "on", "notify", "off":
		return s.AutoUpdate
	}
	if s.DisableUpdateCheck {
		return "off"
	}
	return "on"
}

func DefaultConfig() Config {
	return Config{
		BaseURL:    DefaultBaseURL,
		DefaultKey: "",
		Keys:       make(map[string]string),
		Settings: Settings{
			SendCurrentDirectory:    true,
			SendDirectoryContents:   true,
			SendProjectInstructions: true,
			NumberOfLastCommands:    3,
		},
	}
}

// Dir returns the directory holding CLI state (~/.docsgpt).
func Dir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".docsgpt")
}

// Path returns the config file (~/.docsgpt/config.json).
func Path() string {
	return filepath.Join(Dir(), "config.json")
}

func Load() (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), err
	}
	if cfg.Keys == nil {
		cfg.Keys = make(map[string]string)
	}
	return cfg, nil
}

func (c *Config) Save() error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	// Write a private temp file and rename it over the config: the file can
	// hold a personal access token, so it must never be readable by others
	// (WriteFile keeps the looser mode of a file that already exists until a
	// later chmod) and a crash mid-write must not leave a truncated config.
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, Path())
}

// ResolveURL returns the base URL: override (the --url flag) > DOCSGPT_URL >
// config file > built-in default.
func (c *Config) ResolveURL(override string) string {
	if override != "" {
		return override
	}
	if env := strings.TrimSpace(os.Getenv(EnvURL)); env != "" {
		return env
	}
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

// ResolveToken returns the personal access token and where it came from:
// override (the --token flag) > DOCSGPT_TOKEN > config file. Both are empty
// when no token is configured.
func (c *Config) ResolveToken(override string) (token, source string) {
	if t := strings.TrimSpace(override); t != "" {
		return t, TokenSourceFlag
	}
	if t := strings.TrimSpace(os.Getenv(EnvToken)); t != "" {
		return t, TokenSourceEnv
	}
	if t := strings.TrimSpace(c.Token); t != "" {
		return t, TokenSourceConfig
	}
	return "", ""
}

// RedactToken renders a credential safely for display: the first 15
// characters (the dgpt_pat_ prefix plus the 6 characters the server also shows
// to tell tokens apart) followed by an ellipsis. Short values are fully hidden.
func RedactToken(token string) string {
	const keep = 15
	r := []rune(token)
	if len(r) <= keep {
		return "…"
	}
	return string(r[:keep]) + "…"
}

// ErrNoKey is returned by ResolveKey when no agent API key is configured.
var ErrNoKey = errors.New("no API key")

// ResolveKey returns the agent API key to chat with and a name to show for
// it: the stored key named override (the --key flag) > DOCSGPT_API_KEY >
// the stored default key.
func (c *Config) ResolveKey(override string) (name, key string, err error) {
	if override != "" {
		key, ok := c.Keys[override]
		if !ok {
			return "", "", fmt.Errorf("no stored key named %q (see 'docsgpt-cli login')", override)
		}
		return override, key, nil
	}
	if k := strings.TrimSpace(os.Getenv(EnvAPIKey)); k != "" {
		return EnvAPIKey, k, nil
	}
	if key, ok := c.Keys[c.DefaultKey]; ok && c.DefaultKey != "" {
		return c.DefaultKey, key, nil
	}
	return "", "", ErrNoKey
}

// RedactKey renders an agent API key for display: its first and last four
// characters. Short values are fully hidden.
func RedactKey(key string) string {
	r := []rune(key)
	if len(r) < 16 {
		return "…"
	}
	return string(r[:4]) + "…" + string(r[len(r)-4:])
}

// MigrateIfNeeded checks for old config files and migrates them.
func MigrateIfNeeded() error {
	// If new config already exists, nothing to do
	if _, err := os.Stat(Path()); err == nil {
		return nil
	}

	homeDir, _ := os.UserHomeDir()
	oldKeysFile := filepath.Join(homeDir, ".docsgpt-keys.json")
	oldSettingsFile := filepath.Join(homeDir, ".docsgpt-settings.json")

	cfg := DefaultConfig()
	migrated := false

	// Migrate keys
	if data, err := os.ReadFile(oldKeysFile); err == nil && len(data) > 0 {
		type oldAPIKey struct {
			Key     string `json:"key"`
			Default bool   `json:"default"`
		}
		var oldKeys map[string]oldAPIKey
		if json.Unmarshal(data, &oldKeys) == nil {
			for name, k := range oldKeys {
				cfg.Keys[name] = k.Key
				if k.Default {
					cfg.DefaultKey = name
				}
			}
			migrated = true
		}
	}

	// Migrate settings
	if data, err := os.ReadFile(oldSettingsFile); err == nil && len(data) > 0 {
		if json.Unmarshal(data, &cfg.Settings) == nil {
			migrated = true
		}
	}

	if !migrated {
		return nil
	}

	// Save new config
	if err := cfg.Save(); err != nil {
		return err
	}

	// Rename old files to .bak
	os.Rename(oldKeysFile, oldKeysFile+".bak")
	os.Rename(oldSettingsFile, oldSettingsFile+".bak")

	return nil
}
