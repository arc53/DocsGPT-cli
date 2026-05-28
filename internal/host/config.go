// Package host implements the docsgpt-cli host daemon and pairing flow.
package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HostConfig is the persistent state at ~/.docsgpt/host.yml.
type HostConfig struct {
	DeviceID      string `yaml:"device_id"`
	SessionToken  string `yaml:"session_token"`
	BaseURL       string `yaml:"base_url"`
	PollInterval  string `yaml:"poll_interval"`
	ApprovalMode  string `yaml:"approval_mode"`
	LogFile       string `yaml:"log_file"`
}

const (
	defaultBaseURL      = "https://gptcloud.arc53.com"
	defaultPollInterval = "10s"
	defaultApprovalMode = "ask"
)

// DefaultHostConfig returns the canonical defaults for a fresh host.yml.
func DefaultHostConfig() HostConfig {
	home, _ := os.UserHomeDir()
	return HostConfig{
		BaseURL:      defaultBaseURL,
		PollInterval: defaultPollInterval,
		ApprovalMode: defaultApprovalMode,
		LogFile:      filepath.Join(home, ".docsgpt", "host.log"),
	}
}

func hostConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docsgpt")
}

// HostConfigPath returns the absolute path to host.yml.
func HostConfigPath() string {
	return filepath.Join(hostConfigDir(), "host.yml")
}

// LoadHostConfig reads host.yml; returns the defaults + os.IsNotExist if missing.
func LoadHostConfig() (HostConfig, error) {
	cfg := DefaultHostConfig()
	data, err := os.ReadFile(HostConfigPath())
	if err != nil {
		return cfg, err
	}
	parsed, err := parseSimpleYAML(string(data))
	if err != nil {
		return cfg, fmt.Errorf("parse host.yml: %w", err)
	}
	if v, ok := parsed["device_id"]; ok {
		cfg.DeviceID = v
	}
	if v, ok := parsed["session_token"]; ok {
		cfg.SessionToken = v
	}
	if v, ok := parsed["base_url"]; ok && v != "" {
		cfg.BaseURL = v
	}
	if v, ok := parsed["poll_interval"]; ok && v != "" {
		cfg.PollInterval = v
	}
	if v, ok := parsed["approval_mode"]; ok && v != "" {
		cfg.ApprovalMode = v
	}
	if v, ok := parsed["log_file"]; ok && v != "" {
		cfg.LogFile = v
	}
	return cfg, nil
}

// Save persists the config to disk with 0600 permissions.
func (c *HostConfig) Save() error {
	dir := hostConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir host config: %w", err)
	}
	body := serializeSimpleYAML(map[string]string{
		"device_id":      c.DeviceID,
		"session_token":  c.SessionToken,
		"base_url":       c.BaseURL,
		"poll_interval":  c.PollInterval,
		"approval_mode":  c.ApprovalMode,
		"log_file":       c.LogFile,
	})
	if err := os.WriteFile(HostConfigPath(), []byte(body), 0600); err != nil {
		return fmt.Errorf("write host.yml: %w", err)
	}
	return nil
}

// PollIntervalDuration converts the textual poll_interval into a time.Duration.
func (c *HostConfig) PollIntervalDuration() time.Duration {
	d, err := time.ParseDuration(c.PollInterval)
	if err != nil || d <= 0 {
		return 10 * time.Second
	}
	if d < 5*time.Second {
		return 5 * time.Second
	}
	if d > 60*time.Second {
		return 60 * time.Second
	}
	return d
}

// parseSimpleYAML reads a flat ``key: value`` document. We avoid pulling in
// gopkg.in/yaml.v3 to keep dependencies minimal — host.yml is a flat map.
func parseSimpleYAML(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		value = strings.Trim(value, "\"'")
		out[key] = value
	}
	return out, nil
}

func serializeSimpleYAML(m map[string]string) string {
	order := []string{
		"device_id", "session_token", "base_url",
		"poll_interval", "approval_mode", "log_file",
	}
	var b strings.Builder
	for _, k := range order {
		if v, ok := m[k]; ok {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	return b.String()
}
