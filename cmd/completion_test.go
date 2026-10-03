package cmd

import (
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/config"

	"github.com/spf13/cobra"
)

// TestCompletion calls the completion functions directly: running
// __complete through rootCmd would leave its flags parsed for later tests.
func TestCompletion(t *testing.T) {
	isolateConfig(t)
	cfg := config.DefaultConfig()
	cfg.Keys = map[string]string{"work": "k1", "support": "k2"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	setupCompletion()

	for _, tc := range []struct {
		name string
		f    completeFunc
		args []string
		typed,
		want string
	}{
		{"--key", completeKeyNames, nil, "", "support work"},
		{"logout", logoutCmd.ValidArgsFunction, nil, "", "support work"},
		{"logout name", logoutCmd.ValidArgsFunction, []string{"work"}, "", ""},
		{"config get", configGetCmd.ValidArgsFunction, nil, "auto", "auto_update"},
		{"config set key", configSetCmd.ValidArgsFunction, nil, "re", "retry"},
		{"config set retry", configSetCmd.ValidArgsFunction, []string{"retry"}, "", "on off"},
		{"config set default_key", configSetCmd.ValidArgsFunction, []string{"default_key"}, "w", "work"},
		{"config set url", configSetCmd.ValidArgsFunction, []string{"url"}, "", ""},
		{"a question", rootCmd.ValidArgsFunction, []string{"what", "is"}, "", ""},
	} {
		got, directive := tc.f(rootCmd, tc.args, tc.typed)
		if strings.Join(got, " ") != tc.want || directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("%s: %q (%d), want %q without files", tc.name, got, directive, tc.want)
		}
	}
	if c, _, err := rootCmd.Find([]string{"completion"}); err != nil || c.Name() != "completion" || c.GroupID != "tools" {
		t.Errorf("no completion command under Tools: %v", err)
	}
}
