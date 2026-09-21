package spec

import "testing"

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"https://docsgpt.example.com", "https://docsgpt.example.com/", true},
		{"https://docsgpt.example.com", "https://DOCSGPT.example.com:443/api", true},
		{"http://localhost:7091", "http://localhost:7091/", true},
		{"https://docsgpt.example.com", "http://docsgpt.example.com", false},
		{"https://docsgpt.example.com", "https://evil.example.com", false},
		{"https://docsgpt.example.com", "https://docsgpt.example.com.evil.io", false},
		{"https://docsgpt.example.com", "https://docsgpt.example.com@evil.io", false},
		{"http://localhost:7091", "http://localhost:7092", false},
		{"https://docsgpt.example.com", "", false},
		{"", "", false},
		{"not a url", "not a url", false},
	}
	for _, tt := range tests {
		if got := SameOrigin(tt.a, tt.b); got != tt.want {
			t.Errorf("SameOrigin(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
