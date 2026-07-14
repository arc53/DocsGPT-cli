package config

import "testing"

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
