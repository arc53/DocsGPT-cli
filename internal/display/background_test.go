package display

import "testing"

func TestParseBackground(t *testing.T) {
	tests := []struct {
		reply    string
		dark, ok bool
	}{
		{"\x1b]11;rgb:0000/0000/0000\x07\x1b[?62;22c", true, true},
		{"\x1b]11;rgb:ffff/ffff/ffff\x1b\\", false, true},
		{"\x1b]11;rgb:28/2c/34\x07", true, true}, // two-digit components
		{"\x1b]11;rgba:fdfd/f6f6/e3e3/ffff\x07", false, true},
		{"\x1b[?1;2c", false, false}, // no OSC 11 support
		{"", false, false},
	}
	for _, tt := range tests {
		dark, ok := parseBackground(tt.reply)
		if dark != tt.dark || ok != tt.ok {
			t.Errorf("parseBackground(%q) = %v, %v; want %v, %v", tt.reply, dark, ok, tt.dark, tt.ok)
		}
	}
}

func TestColorFgBgDark(t *testing.T) {
	tests := []struct {
		v        string
		dark, ok bool
	}{
		{"15;0", true, true},
		{"0;15", false, true},
		{"12;8", true, true},
		{"0;default;7", false, true},
		{"default;default", false, false},
		{"", false, false},
		{"7;255", false, false},
	}
	for _, tt := range tests {
		dark, ok := colorFgBgDark(tt.v)
		if dark != tt.dark || ok != tt.ok {
			t.Errorf("colorFgBgDark(%q) = %v, %v; want %v, %v", tt.v, dark, ok, tt.dark, tt.ok)
		}
	}
}
