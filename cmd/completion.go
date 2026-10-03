package cmd

import (
	"os"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/config"

	"github.com/spf13/cobra"
)

// completeFunc completes the argument being typed.
type completeFunc = func(cmd *cobra.Command, args []string, typed string) ([]string, cobra.ShellCompDirective)

// setupCompletion adds cobra's completion command (under Tools) and
// completes what the config knows: key names (--key, logout, default_key)
// and settings with their values. Nothing goes over the network.
func setupCompletion() {
	rootCmd.InitDefaultCompletionCmd()
	for _, c := range rootCmd.Commands() {
		if c.Name() == "completion" {
			c.GroupID = "tools"
			c.Short = "Print the shell completion script (bash, zsh, fish, powershell)"
		}
	}

	noFiles := words(nil)
	rootCmd.ValidArgsFunction = noFiles // a question
	askCmd.ValidArgsFunction = noFiles
	chatCmd.ValidArgsFunction = noFiles
	rootCmd.RegisterFlagCompletionFunc("key", completeKeyNames)
	rootCmd.RegisterFlagCompletionFunc("url", noFiles)
	rootCmd.RegisterFlagCompletionFunc("token", noFiles)
	logoutCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, typed string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return noFiles(cmd, args, typed)
		}
		return completeKeyNames(cmd, args, typed)
	}
	configGetCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, typed string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return noFiles(cmd, args, typed)
		}
		return words(settingKeys())(cmd, args, typed)
	}
	configSetCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, typed string) ([]string, cobra.ShellCompDirective) {
		switch len(args) {
		case 0:
			return words(settingKeys())(cmd, args, typed)
		case 1:
			if args[0] == "default_key" {
				return completeKeyNames(cmd, nil, typed)
			}
			if s, err := lookupSetting(args[0]); err == nil {
				return words(s.values)(cmd, args, typed)
			}
		}
		return noFiles(cmd, args, typed)
	}
}

// words completes the words of list that start with what is typed, and
// never file names.
func words(list []string) completeFunc {
	return func(_ *cobra.Command, _ []string, typed string) ([]string, cobra.ShellCompDirective) {
		var out []string
		for _, w := range list {
			if strings.HasPrefix(w, typed) {
				out = append(out, w)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeKeyNames completes the names of the stored API keys.
func completeKeyNames(cmd *cobra.Command, args []string, typed string) ([]string, cobra.ShellCompDirective) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return words(sortedNames(cfg.Keys))(cmd, args, typed)
}

func settingKeys() []string {
	keys := make([]string, len(settings))
	for i, s := range settings {
		keys[i] = s.key
	}
	return keys
}

// completing reports whether the shell runs this process to complete a
// command line, which must print nothing but the completions.
func completing() bool {
	return len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "__complete")
}
