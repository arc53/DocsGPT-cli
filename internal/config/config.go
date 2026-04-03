package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultBaseURL = "https://gptcloud.arc53.com"

type Config struct {
	BaseURL    string            `json:"base_url"`
	DefaultKey string            `json:"default_key"`
	Keys       map[string]string `json:"keys"`
	Settings   Settings          `json:"settings"`
}

type Settings struct {
	SendCurrentDirectory  bool `json:"send_current_directory"`
	SendDirectoryContents bool `json:"send_directory_contents"`
	SendLastCommands      bool `json:"send_last_commands"`
	NumberOfLastCommands  int  `json:"number_of_last_commands"`
}

func DefaultConfig() Config {
	return Config{
		BaseURL:    DefaultBaseURL,
		DefaultKey: "",
		Keys:       make(map[string]string),
		Settings: Settings{
			SendCurrentDirectory:  true,
			SendDirectoryContents: true,
			SendLastCommands:      true,
			NumberOfLastCommands:  3,
		},
	}
}

func configDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".docsgpt")
}

func configPath() string {
	return filepath.Join(configDir(), "config.json")
}

func Load() (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(configPath())
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
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	return os.WriteFile(configPath(), data, 0600)
}

func (c *Config) ActiveKey() (string, error) {
	if c.DefaultKey == "" {
		return "", fmt.Errorf("no default key set. Use 'keys' to set one")
	}
	key, ok := c.Keys[c.DefaultKey]
	if !ok {
		return "", fmt.Errorf("default key %q not found in keys", c.DefaultKey)
	}
	return key, nil
}

// ResolveURL returns the base URL, with an override taking precedence.
func (c *Config) ResolveURL(override string) string {
	if override != "" {
		return override
	}
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

// ResolveKey returns the API key value, with a named override taking precedence.
func (c *Config) ResolveKey(overrideName string) (string, string, error) {
	name := c.DefaultKey
	if overrideName != "" {
		name = overrideName
	}
	if name == "" {
		return "", "", fmt.Errorf("no key specified. Use 'keys' to add one")
	}
	key, ok := c.Keys[name]
	if !ok {
		return "", "", fmt.Errorf("key %q not found", name)
	}
	return name, key, nil
}

// MigrateIfNeeded checks for old config files and migrates them.
func MigrateIfNeeded() error {
	// If new config already exists, nothing to do
	if _, err := os.Stat(configPath()); err == nil {
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
