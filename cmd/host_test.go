package cmd

import "testing"

func TestParsePairMenuChoice(t *testing.T) {
	const numOptions = 3
	const defaultIdx = 2 // "Nothing for now" when 3 options

	tests := []struct {
		name    string
		input   string
		wantIdx int
		wantOK  bool
	}{
		{"empty selects default", "", defaultIdx, true},
		{"whitespace only selects default", "   ", defaultIdx, true},
		{"valid first option", "1", 0, true},
		{"valid middle option", "2", 1, true},
		{"valid last option", "3", 2, true},
		{"valid with surrounding spaces", "  2  ", 1, true},
		{"valid with trailing newline", "1\n", 0, true},
		{"zero is out of range", "0", 0, false},
		{"too high is out of range", "4", 0, false},
		{"negative is out of range", "-1", 0, false},
		{"non-numeric", "abc", 0, false},
		{"numeric with junk", "1x", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			idx, ok := parsePairMenuChoice(tc.input, numOptions, defaultIdx)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (input %q)", ok, tc.wantOK, tc.input)
			}
			if ok && idx != tc.wantIdx {
				t.Fatalf("idx = %d, want %d (input %q)", idx, tc.wantIdx, tc.input)
			}
		})
	}
}

func TestBuildPairMenuActions(t *testing.T) {
	// Platforms with an install-service backend (systemd / launchd) include
	// the install option.
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos+" includes install option", func(t *testing.T) {
			actions := buildPairMenuActions(goos)
			want := []string{pairMenuStart, pairMenuInstall, pairMenuNothing}
			if len(actions) != len(want) {
				t.Fatalf("len = %d, want %d (%v)", len(actions), len(want), actions)
			}
			for i := range want {
				if actions[i] != want[i] {
					t.Fatalf("actions[%d] = %q, want %q", i, actions[i], want[i])
				}
			}
		})
	}

	// Unsupported platforms omit the install option.
	for _, goos := range []string{"windows", "freebsd"} {
		t.Run(goos+" omits install option", func(t *testing.T) {
			actions := buildPairMenuActions(goos)
			want := []string{pairMenuStart, pairMenuNothing}
			if len(actions) != len(want) {
				t.Fatalf("len = %d, want %d (%v)", len(actions), len(want), actions)
			}
			for i := range want {
				if actions[i] != want[i] {
					t.Fatalf("actions[%d] = %q, want %q", i, actions[i], want[i])
				}
			}
			for _, a := range actions {
				if a == pairMenuInstall {
					t.Fatalf("install option should be absent on %s", goos)
				}
			}
		})
	}
}

// TestBuildPairMenuActionsDefaultIsLast guards the invariant the menu relies
// on: the safe default ("Nothing for now") is always the final option.
func TestBuildPairMenuActionsDefaultIsLast(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		actions := buildPairMenuActions(goos)
		if last := actions[len(actions)-1]; last != pairMenuNothing {
			t.Fatalf("%s: last action = %q, want %q", goos, last, pairMenuNothing)
		}
	}
}
