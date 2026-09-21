package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/manage"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

var whoamiJSON bool

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Store a DocsGPT personal access token for the account-level commands",
	Long: `Validate a personal access token against the server and store it in
~/.docsgpt/config.json (mode 0600).

Create the token in the DocsGPT web app (Settings -> Access Tokens) with the
scopes your workflow needs, e.g. agents:write + sources:write for deployments
or chat:run for benchmarks. The token is read from --token, else from stdin
when piped, else from a hidden prompt:

  docsgpt-cli login                          # hidden prompt
  echo "$DOCSGPT_TOKEN" | docsgpt-cli login  # from stdin
  docsgpt-cli login --token dgpt_pat_...     # from a flag (visible in shell history)

CI jobs do not need login at all: set DOCSGPT_TOKEN (and DOCSGPT_URL).
With --url, the base URL is stored alongside the token.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		token, err := readLoginToken(globalToken, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return runLogin(ctx, token, globalURL, os.Stdout)
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored personal access token",
	Long: `Remove the personal access token from ~/.docsgpt/config.json.

This only forgets the token locally; revoke it in the DocsGPT web app to
invalidate it.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLogout(os.Stdout)
	},
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the account and scopes of the active personal access token",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return runWhoami(ctx, whoamiJSON, os.Stdout)
	},
}

func init() {
	for _, c := range []*cobra.Command{loginCmd, logoutCmd, whoamiCmd} {
		markManagement(c)
	}
	whoamiCmd.Flags().BoolVar(&whoamiJSON, "json", false, "Print the server's /api/user/me document as JSON")
}

// readLoginToken picks the token: flag > piped stdin > hidden prompt.
func readLoginToken(flagToken string, stdin *os.File, prompt io.Writer) (string, error) {
	if t := strings.TrimSpace(flagToken); t != "" {
		return t, nil
	}
	if !term.IsTerminal(stdin.Fd()) {
		line, err := bufio.NewReader(io.LimitReader(stdin, 4096)).ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read token from stdin: %w", err)
		}
		if t := strings.TrimSpace(line); t != "" {
			return t, nil
		}
		return "", usageErrf("no token provided: pass --token or pipe the token on stdin")
	}
	fmt.Fprint(prompt, "Personal access token: ")
	raw, err := term.ReadPassword(stdin.Fd())
	fmt.Fprintln(prompt)
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	if t := strings.TrimSpace(string(raw)); t != "" {
		return t, nil
	}
	return "", usageErrf("no token provided")
}

// runLogin validates token against GET /api/user/me and stores it. A non-empty
// urlFlag (--url) is stored as the base URL too, since a token belongs to one
// deployment.
func runLogin(ctx context.Context, token, urlFlag string, out io.Writer) error {
	if !strings.HasPrefix(token, config.TokenPrefix) {
		return usageErrf("that does not look like a personal access token (expected it to start with %q)", config.TokenPrefix)
	}
	cfg, err := config.Load()
	if err != nil {
		return usageErrf("load config: %w", err)
	}
	baseURL := cfg.ResolveURL(urlFlag)
	id, err := manage.New(baseURL, token, userAgent()).Me(ctx)
	if err != nil {
		return fmt.Errorf("token was not accepted by %s: %w", baseURL, err)
	}

	// Store the token together with the server that accepted it, wherever that
	// URL came from (--url, DOCSGPT_URL or the config). Otherwise a later run
	// without DOCSGPT_URL would send the token to a different server.
	cfg.Token = token
	cfg.BaseURL = strings.TrimRight(baseURL, "/")
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintln(out, display.Success("Logged in.")+" Token stored in ~/.docsgpt/config.json")
	printIdentity(out, id, token, config.TokenSourceConfig, baseURL)
	return nil
}

func runLogout(out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return usageErrf("load config: %w", err)
	}
	if cfg.Token == "" {
		fmt.Fprintln(out, "No stored token.")
	} else {
		cfg.Token = ""
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintln(out, display.Success("Logged out.")+" The stored token was removed (revoke it in the web app to invalidate it).")
	}
	if os.Getenv(config.EnvToken) != "" {
		warnLine(out, config.EnvToken+" is still set in this environment and will keep being used")
	}
	return nil
}

func runWhoami(ctx context.Context, asJSON bool, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return usageErrf("load config: %w", err)
	}
	token, source := cfg.ResolveToken(globalToken)
	if token == "" {
		return usageErrf("no personal access token configured: run 'docsgpt-cli login', set %s, or pass --token", config.EnvToken)
	}
	baseURL := cfg.ResolveURL(globalURL)
	id, err := manage.New(baseURL, token, userAgent()).Me(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, id.Raw)
	}
	printIdentity(out, id, token, source, baseURL)
	return nil
}

// printIdentity renders the /api/user/me document. The token is only ever
// shown redacted.
func printIdentity(out io.Writer, id *manage.Identity, token, source, baseURL string) {
	row := func(k, v string) { fmt.Fprintf(out, "  %-13s %s\n", k, v) }
	row("Server", baseURL)
	row("User", textOrDash(id.UserID))
	if id.Email != "" {
		row("Email", id.Email)
	}
	if len(id.Roles) > 0 {
		row("Roles", strings.Join(id.Roles, ", "))
	}
	row("Token", fmt.Sprintf("%s (from %s)", config.RedactToken(token), source))
	if id.Token == nil {
		row("Auth method", textOrDash(id.AuthMethod))
		return
	}
	row("Token name", textOrDash(id.Token.Name))
	row("Token id", textOrDash(id.Token.ID))
	scopes := append([]string(nil), id.Token.Scopes...)
	sort.Strings(scopes)
	if len(scopes) == 0 {
		row("Scopes", "(none)")
	} else {
		row("Scopes", strings.Join(scopes, ", "))
	}
	if len(id.Token.ResourceFilter) == 0 {
		row("Restrictions", "none (all resources of the granted scopes)")
		return
	}
	families := make([]string, 0, len(id.Token.ResourceFilter))
	for f := range id.Token.ResourceFilter {
		families = append(families, f)
	}
	sort.Strings(families)
	for i, f := range families {
		label := ""
		if i == 0 {
			label = "Restrictions"
		}
		row(label, fmt.Sprintf("%s: %s", f, strings.Join(id.Token.ResourceFilter[f], ", ")))
	}
}
