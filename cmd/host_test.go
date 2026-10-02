package cmd

import "testing"

func TestBuildPairMenuActions(t *testing.T) {
	// Platforms with an install-service backend (systemd / launchd /
	// Task Scheduler) include the install option.
	for _, goos := range []string{"linux", "darwin", "windows"} {
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
	for _, goos := range []string{"freebsd", "openbsd"} {
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
