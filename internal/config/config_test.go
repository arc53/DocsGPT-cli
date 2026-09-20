package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAutoUpdateMode(t *testing.T) {
	tests := []struct {
		name     string
		settings Settings
		want     string
	}{
		{"default", Settings{}, "on"},
		{"explicit on", Settings{AutoUpdate: "on"}, "on"},
		{"notify", Settings{AutoUpdate: "notify"}, "notify"},
		{"off", Settings{AutoUpdate: "off"}, "off"},
		{"legacy disable", Settings{DisableUpdateCheck: true}, "off"},
		{"explicit wins over legacy", Settings{AutoUpdate: "on", DisableUpdateCheck: true}, "on"},
		{"invalid falls back", Settings{AutoUpdate: "sometimes"}, "on"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.settings.AutoUpdateMode(); got != tt.want {
				t.Errorf("AutoUpdateMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

// useTempHome points the config at an empty home directory and clears the
// environment variables the resolvers read.
func useTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvToken, "")
	t.Setenv(EnvURL, "")
	return home
}

func TestResolveTokenPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		flag       string
		env        string
		stored     string
		wantToken  string
		wantSource string
	}{
		{"nothing configured", "", "", "", "", ""},
		{"config only", "", "", "dgpt_pat_cfg", "dgpt_pat_cfg", TokenSourceConfig},
		{"env beats config", "", "dgpt_pat_env", "dgpt_pat_cfg", "dgpt_pat_env", TokenSourceEnv},
		{"flag beats env and config", "dgpt_pat_flag", "dgpt_pat_env", "dgpt_pat_cfg", "dgpt_pat_flag", TokenSourceFlag},
		{"whitespace is trimmed", "", "  dgpt_pat_env\n", "", "dgpt_pat_env", TokenSourceEnv},
		{"blank env falls through", "", "   ", "dgpt_pat_cfg", "dgpt_pat_cfg", TokenSourceConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvToken, tt.env)
			cfg := DefaultConfig()
			cfg.Token = tt.stored
			token, source := cfg.ResolveToken(tt.flag)
			if token != tt.wantToken || source != tt.wantSource {
				t.Errorf("ResolveToken() = %q, %q; want %q, %q", token, source, tt.wantToken, tt.wantSource)
			}
		})
	}
}

func TestResolveURLPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		flag   string
		env    string
		stored string
		want   string
	}{
		{"default", "", "", "", DefaultBaseURL},
		{"config", "", "", "https://cfg.example", "https://cfg.example"},
		{"env beats config", "", "https://env.example", "https://cfg.example", "https://env.example"},
		{"flag beats env", "https://flag.example", "https://env.example", "https://cfg.example", "https://flag.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvURL, tt.env)
			cfg := Config{BaseURL: tt.stored}
			if got := cfg.ResolveURL(tt.flag); got != tt.want {
				t.Errorf("ResolveURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedactToken(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"dgpt_pat_AbCdEf0123456789abcdefghijklmnop", "dgpt_pat_AbCdEf…"},
		{"short", "…"},
		{"", "…"},
	}
	for _, tt := range tests {
		got := RedactToken(tt.in)
		if got != tt.want {
			t.Errorf("RedactToken(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if len(tt.in) > 15 && strings.Contains(got, tt.in[15:]) {
			t.Errorf("RedactToken leaks the secret part: %q", got)
		}
	}
}

func TestTokenPersistenceAndPermissions(t *testing.T) {
	home := useTempHome(t)
	path := filepath.Join(home, ".docsgpt", "config.json")

	// An existing, too-permissive config written by an older version: no
	// token field, keys and settings present.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"base_url":"https://self.example","default_key":"main","keys":{"main":"k-123"},"settings":{"number_of_last_commands":7,"auto_update":"notify"}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "" || cfg.Keys["main"] != "k-123" || cfg.Settings.NumberOfLastCommands != 7 {
		t.Fatalf("legacy config not loaded intact: %+v", cfg)
	}

	cfg.Token = "dgpt_pat_secret"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := st.Mode().Perm(); perm != 0o600 {
			t.Errorf("config mode = %o, want 600 (an existing 644 file must be tightened)", perm)
		}
	}

	// Save goes through a temp file; none may be left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("unexpected file left in the config dir: %s", e.Name())
		}
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Token != "dgpt_pat_secret" || reloaded.BaseURL != "https://self.example" ||
		reloaded.DefaultKey != "main" || reloaded.Keys["main"] != "k-123" ||
		reloaded.Settings.AutoUpdate != "notify" || reloaded.Settings.NumberOfLastCommands != 7 {
		t.Errorf("round trip lost data: %+v", reloaded)
	}

	// Removing the token drops the field from the file entirely.
	reloaded.Token = ""
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "token") || strings.Contains(string(data), "dgpt_pat_") {
		t.Errorf("token still on disk: %s", data)
	}
}

func TestMigrationStillWorksWithTokenField(t *testing.T) {
	home := useTempHome(t)
	oldKeys := filepath.Join(home, ".docsgpt-keys.json")
	if err := os.WriteFile(oldKeys, []byte(`{"main":{"key":"k-1","default":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateIfNeeded(); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultKey != "main" || cfg.Keys["main"] != "k-1" || cfg.Token != "" {
		t.Errorf("migrated config = %+v", cfg)
	}
	if _, err := os.Stat(oldKeys + ".bak"); err != nil {
		t.Errorf("old keys file not renamed: %v", err)
	}
}
