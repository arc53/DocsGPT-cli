package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/config"
)

func TestConfigSet(t *testing.T) {
	isolateConfig(t)
	for _, tt := range []struct{ key, value string }{
		{"url", "http://localhost:7091/"},
		{"auto_update", "Notify"},
		{"send_last_commands", "off"},
		{"number_of_last_commands", "5"},
		{"theme", "light"},
	} {
		if err := setConfig(tt.key, tt.value); err != nil {
			t.Fatalf("set %s %s: %v", tt.key, tt.value, err)
		}
	}
	cfg, _ := config.Load()
	s := cfg.Settings
	if cfg.BaseURL != "http://localhost:7091" || s.AutoUpdate != "notify" || s.SendLastCommands || s.NumberOfLastCommands != 5 || s.Theme != "light" {
		t.Errorf("stored config = %+v", cfg)
	}

	for _, tt := range []struct{ key, value string }{
		{"url", "localhost:7091"},
		{"auto_update", "sometimes"},
		{"number_of_last_commands", "-1"},
		{"default_key", "missing"},
		{"colour", "red"},
	} {
		if err := setConfig(tt.key, tt.value); exitCodeFor(err) != exitUsage {
			t.Errorf("set %s %s: err = %v, want a usage error", tt.key, tt.value, err)
		}
	}

	// The old set-* commands still work.
	if err := runRoot(t, "config", "set-banner", "never"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = config.Load(); cfg.Settings.Banner != "never" {
		t.Errorf("banner = %q", cfg.Settings.Banner)
	}
}

func TestConfigShowRedacts(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Keys = map[string]string{"main": "a1b2c3d4-SECRET-SECRET-0000aaaa9999"}
	cfg.DefaultKey = "main"
	cfg.Token = cmdTestToken
	for _, asJSON := range []bool{false, true} {
		var out bytes.Buffer
		if err := runConfigShow(cfg, asJSON, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "SECRET") || !strings.Contains(out.String(), "a1b2…9999") || !strings.Contains(out.String(), "dgpt_pat_AbCdEf…") {
			t.Errorf("show (json=%v):\n%s", asJSON, out.String())
		}
	}
}
