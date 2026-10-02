package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/install"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	"github.com/arc53/DocsGPT-cli/internal/update"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Version is stamped at link time (see the Makefile and .goreleaser.yaml).
// It stays "dev" for a plain `go build`, and resolveVersion fills it in for a
// `go install` build, which carries no ldflags.
var Version = "dev"

// resolveVersion recovers the version of a binary built by `go install
// github.com/arc53/DocsGPT-cli/cmd/docsgpt-cli@vX.Y.Z`, which the Go toolchain
// records in the build info but cannot stamp with ldflags. Without it such a
// build reports "dev", which IsReleaseVersion rejects — so it would never check
// for updates and `docsgpt-cli update` would refuse to run.
func resolveVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return versionFromBuildInfo(info)
}

// versionFromBuildInfo returns the release version a binary was installed at,
// or "" when it was not installed from the module cache at a release version.
//
// Go also stamps Main.Version from VCS tags, so `go build` on a clean checkout
// of v1.5.1 reports v1.5.1 and would otherwise be indistinguishable from a
// released binary — auto-update would then replace a developer's local build
// with the latest release behind their back. A module-cache build carries a
// checksum in Main.Sum; one built from a working tree never does, and carries
// vcs.* build settings instead.
func versionFromBuildInfo(info *debug.BuildInfo) string {
	if info.Main.Sum == "" {
		return ""
	}
	for _, setting := range info.Settings {
		if strings.HasPrefix(setting.Key, "vcs") {
			return ""
		}
	}
	if !update.IsReleaseVersion(info.Main.Version) {
		return ""
	}
	return info.Main.Version
}

// Global flags (every command).
var (
	globalURL   string
	globalKey   string
	globalToken string
	globalTheme string // hidden; the theme setting is the documented way
)

// Flags of the chat commands (the root, ask and chat).
var (
	globalNoStream    bool
	globalNoContext   bool
	globalNoTools     bool
	globalAutoApprove bool
	globalTimeout     int // seconds a tool command may run
)

// startupConfig is the config as it was when the process started, for the
// update gate and the theme; commands load their own copy to change it.
var (
	startupConfig    = config.DefaultConfig()
	startupConfigErr error
)

var rootCmd = &cobra.Command{
	Use:     "docsgpt-cli [question]",
	Version: Version,
	Short:   "Chat with your DocsGPT agents from the terminal",
	Long: `Chat with your DocsGPT agents from the terminal. With no arguments it opens an
interactive chat; with a question it answers once and exits. Anything piped in
is sent along with the question.`,
	Example: `  docsgpt-cli                                # chat
  docsgpt-cli "how do I rotate the API key?" # ask once
  git diff | docsgpt-cli "review this"       # send piped input along
  docsgpt-cli login                          # add an agent API key`,
	Args: cobra.ArbitraryArgs,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Theme: flag > config > auto (which asks the terminal).
		theme := globalTheme
		if theme == "" {
			theme = startupConfig.Settings.Theme
		}
		display.InitTheme(theme)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && stdinIsTerminal() && isatty.IsTerminal(os.Stdout.Fd()) {
			return chatCmd.RunE(chatCmd, nil)
		}
		if err := commandTypo(cmd, args); err != nil {
			return err
		}
		return askCmd.RunE(askCmd, args)
	},
	SilenceErrors: true,
	SilenceUsage:  true,
}

// commandTypo keeps a mistyped command from being sent as a question: a lone
// word close to a command name, or such a word followed by one of that
// command's subcommands ("agnets list"). A quoted question, or anything after
// --, is never taken for a command.
func commandTypo(cmd *cobra.Command, args []string) error {
	if len(args) == 0 || cmd.ArgsLenAtDash() == 0 || strings.ContainsAny(args[0], " \t\n") {
		return nil
	}
	for _, name := range suggestions(cmd, args[0]) {
		sub, _, err := cmd.Find([]string{name})
		if err != nil {
			continue
		}
		if len(args) == 1 || len(suggestions(sub, args[1])) > 0 {
			return usageErrf("unknown command %q, did you mean %q?\n%s", args[0], name, questionHint(args))
		}
	}
	return nil
}

func questionHint(words []string) string {
	return fmt.Sprintf("To ask it as a question: docsgpt-cli -- %q", strings.Join(words, " "))
}

// questionArgs makes extra words after a top-level command ("update my
// nginx config") a usage error that says how to ask them as a question.
func questionArgs() {
	for _, c := range rootCmd.Commands() {
		v := c.Args
		if v == nil && c.HasSubCommands() {
			v = subcommandArgs
		}
		if v == nil {
			continue
		}
		c.Args = func(cmd *cobra.Command, args []string) error {
			if err := v(cmd, args); err != nil {
				return usageErrf("%w\n%s", err, questionHint(append([]string{cmd.CalledAs()}, args...)))
			}
			return nil
		}
	}
}

// subcommandArgs rejects arguments on a command that only groups others,
// suggesting the closest subcommand.
func subcommandArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	if s := suggestions(cmd, args[0]); len(s) > 0 {
		msg += fmt.Sprintf(", did you mean %q?", s[0])
	}
	return usageErrf("%s\nRun '%s --help' for usage.", msg, cmd.CommandPath())
}

// suggestions lists the subcommands of cmd that typed is a prefix or a
// near miss (2 edits) of, like cobra's own "did you mean" hints.
func suggestions(cmd *cobra.Command, typed string) []string {
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	return cmd.SuggestionsFor(typed)
}

// groupCommand makes c, which only holds subcommands, print its help when run
// bare and reject unknown subcommands (exit 2) instead of ignoring them.
func groupCommand(c *cobra.Command) {
	c.Args = subcommandArgs
	c.RunE = func(cmd *cobra.Command, args []string) error { return cmd.Help() }
	c.Annotations = map[string]string{groupAnnotation: "true"}
}

const groupAnnotation = "docsgpt/group"

func Execute() {
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	questionArgs()

	if err := config.MigrateIfNeeded(); err != nil {
		display.ErrorMsg(err.Error())
		os.Exit(exitFailure)
	}
	startupConfig, startupConfigErr = config.Load()

	mode, exePath := updateGate()
	if mode == update.ModeOn {
		if v, err := update.ApplyStaged(Version, exePath); err == nil && v != "" {
			fmt.Fprintln(os.Stderr, display.Muted(
				"docsgpt-cli updated to "+v+" (takes effect on your next command)"))
		}
	}
	if mode != "" && update.ShouldSpawnWorker(Version, mode) {
		update.SpawnWorker()
	}

	err := rootCmd.Execute()

	if mode == update.ModeNotify {
		if latest := update.CachedNotice(Version); latest != "" {
			fmt.Fprintln(os.Stderr, display.Muted(fmt.Sprintf(
				"\nA new version is available: %s → %s. Run 'docsgpt-cli update'.", Version, latest)))
		}
	}

	if err != nil {
		// A prompt dismissed with Esc or Ctrl+C needs no message.
		if !errors.Is(err, ui.ErrCancelled) {
			display.ErrorMsg(err.Error())
		}
		os.Exit(exitCodeFor(err))
	}
}

// updateGate decides how the passive update machinery behaves for this
// invocation. "" disables it entirely: opted out, no TTY, a non-release
// build, or the update/host commands (which run their own update logic).
// Installs we cannot swap (Homebrew, unwritable dir) downgrade on → notify.
func updateGate() (mode string, exePath string) {
	if os.Getenv("DOCSGPT_NO_UPDATE_CHECK") != "" {
		return "", ""
	}
	if !isatty.IsTerminal(os.Stderr.Fd()) {
		return "", ""
	}
	if cmd, _, err := rootCmd.Find(os.Args[1:]); err == nil && (cmd == updateCmd || cmd == hostCmd) {
		return "", ""
	}
	if !update.IsReleaseVersion(Version) {
		return "", ""
	}
	if startupConfigErr != nil {
		return "", ""
	}
	mode = startupConfig.Settings.AutoUpdateMode()
	if mode == update.ModeOff {
		return "", ""
	}

	exe, err := os.Executable()
	if err != nil {
		return "", ""
	}
	exePath, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", ""
	}
	if mode == update.ModeOn && (update.IsHomebrewPath(exePath) || !install.IsWritable(filepath.Dir(exePath))) {
		mode = update.ModeNotify
	}
	return mode, exePath
}

func init() {
	// Runs after the package-level vars, so rootCmd.Version has already been
	// copied from the unresolved Version and has to be refreshed here too.
	if Version == "dev" {
		if v := resolveVersion(); v != "" {
			Version = v
		}
	}
	rootCmd.Version = Version

	pf := rootCmd.PersistentFlags()
	pf.StringVar(&globalURL, "url", "", "DocsGPT server URL (default: config, else "+config.DefaultBaseURL+")")
	pf.StringVar(&globalKey, "key", "", "Use the stored API key of this name")
	pf.StringVar(&globalToken, "token", "", "Personal access token (dgpt_pat_…) for account commands")
	pf.StringVar(&globalTheme, "theme", "", "Color theme: auto, dark, light")
	pf.MarkHidden("theme")

	for _, c := range []*cobra.Command{rootCmd, askCmd, chatCmd} {
		f := c.Flags()
		f.BoolVar(&globalNoStream, "no-stream", false, "Print the answer once it is complete")
		f.BoolVar(&globalNoContext, "no-context", false, "Don't send the working directory, its files, AGENTS.md or shell history")
		f.BoolVar(&globalNoTools, "no-tools", false, "Don't let the agent run commands or read and write files")
		f.BoolVar(&globalAutoApprove, "auto-approve", false, "Run the agent's tool calls without asking")
		f.IntVar(&globalTimeout, "tool-timeout", 30, "Seconds a command run by the agent may take")
		f.Bool("no-motion", false, "")
		f.MarkDeprecated("no-motion", "the banner no longer animates")
	}
	for _, c := range []*cobra.Command{askCmd, chatCmd} {
		c.Flags().IntVar(&globalTimeout, "timeout", 30, "")
		c.Flags().MarkDeprecated("timeout", "use --tool-timeout")
	}

	// No usage dump on runtime errors; flag errors exit 2 with a pointer to
	// --help.
	rootCmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return usageErrf("%w\nRun '%s --help' for usage.", err, c.CommandPath())
	})
	cobra.AddTemplateFunc("namePadding", func(c *cobra.Command) int {
		n := 9
		for _, sib := range c.Parent().Commands() {
			if sib.IsAvailableCommand() {
				n = max(n, len(sib.Name()))
			}
		}
		return n + 2
	})
	rootCmd.SetUsageTemplate(usageTemplate)
	cobra.EnableCommandSorting = false

	rootCmd.AddGroup(
		&cobra.Group{ID: "account", Title: "Account & agents:"},
		&cobra.Group{ID: "tools", Title: "Tools:"},
		&cobra.Group{ID: "host", Title: "Host mode:"},
	)
	for _, c := range []*cobra.Command{loginCmd, logoutCmd, whoamiCmd, agentsCmd, sourcesCmd} {
		c.GroupID = "account"
	}
	for _, c := range []*cobra.Command{benchCmd, configCmd, updateCmd} {
		c.GroupID = "tools"
	}
	hostCmd.GroupID = "host"
	rootCmd.AddCommand(askCmd, chatCmd, loginCmd, logoutCmd, whoamiCmd, keysCmd, agentsCmd, sourcesCmd,
		promptsCmd, toolsCmd, benchCmd, configCmd, updateCmd, installCmd, hostCmd)
}

// usageTemplate is cobra's, minus the help command, the "Additional
// Commands" section and the padding that hidden aliases would bring back,
// and the "<cmd> [flags]" line of commands that only group others.
const usageTemplate = `Usage:{{if and .Runnable (not (index .Annotations "` + groupAnnotation + `"))}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} <command>{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Commands:{{range $cmds}}{{if .IsAvailableCommand}}
  {{rpad .Name (namePadding .)}} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) .IsAvailableCommand)}}
  {{rpad .Name (namePadding .)}} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Run '{{.CommandPath}} <command> --help' for more about a command.{{end}}
`
