package cmd

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/manage"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	"github.com/spf13/cobra"
)

var (
	whoamiJSON  bool
	loginName   string
	logoutToken tokenSwitch
	logoutAll   bool
	logoutYes   bool
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Add an agent API key or a personal access token",
	Long: `Check a credential with the server and store it in ~/.docsgpt/config.json.

  Agent API key          chats with one agent (DocsGPT → Agent settings → API key)
  Personal access token  dgpt_pat_…, for agents, sources and bench agent_id runs
                         (DocsGPT → Settings → Access tokens)

On a terminal, login lists the stored keys to switch the default one, or adds
a new key. Otherwise it reads the credential from stdin, and a new agent key
becomes the default.

Credentials are picked in this order: --key <name> > DOCSGPT_API_KEY > the
default key, and --token > DOCSGPT_TOKEN > the stored token. CI jobs can set
those variables (and DOCSGPT_URL) instead of logging in.`,
	Example: `  docsgpt-cli login
  echo "$AGENT_KEY" | docsgpt-cli login --name support
  echo "$DOCSGPT_TOKEN" | docsgpt-cli login`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if t := strings.TrimSpace(globalToken); t != "" {
			return runLogin(ctx, t, globalURL, os.Stdout)
		}
		if ui.Interactive() {
			return loginInteractive(ctx, os.Stdout)
		}
		if stdinIsTerminal() {
			return usageErrf("pipe the key or token on stdin, or run login in a terminal")
		}
		secret, err := readPipedSecret(os.Stdin)
		if err != nil {
			return err
		}
		if strings.HasPrefix(secret, config.TokenPrefix) {
			return runLogin(ctx, secret, globalURL, os.Stdout)
		}
		return loginKey(ctx, secret, loginName, os.Stdout)
	},
}

// keysCmd is the old key manager, now the login picker.
var keysCmd = &cobra.Command{
	Use:    "keys",
	Short:  "Pick the default API key or add one (same as login)",
	Hidden: true,
	Args:   usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !ui.Interactive() {
			return usageErrf("keys is now part of login: run 'docsgpt-cli login' in a terminal, or see 'docsgpt-cli login --help'")
		}
		return loginInteractive(context.Background(), os.Stdout)
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout [name]",
	Short: "Remove a stored API key or the access token",
	Long: `Remove credentials from ~/.docsgpt/config.json. Without arguments, logout
lists them to pick from. This only forgets them locally: revoke them in DocsGPT
to invalidate them.`,
	Example: `  docsgpt-cli logout
  docsgpt-cli logout support --yes
  docsgpt-cli logout --token --yes
  docsgpt-cli logout --all --yes`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLogout(args, os.Stdout)
	},
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the active API key and access token",
	Long: `Show the agent API key that chat and questions use, with the agent it
belongs to, and the access token's user, scopes and restrictions. Exits 1 when
neither is configured.`,
	Example: `  docsgpt-cli whoami
  docsgpt-cli whoami --key support
  docsgpt-cli whoami --json | jq -r '.token.scopes[]'`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return runWhoami(ctx, whoamiJSON, os.Stdout)
	},
}

func init() {
	loginCmd.Flags().StringVar(&loginName, "name", "", "Name for a piped agent key (default: the agent's name)")
	logoutCmd.Flags().Var(&logoutToken, "token", "Remove the stored personal access token")
	logoutCmd.Flags().Lookup("token").NoOptDefVal = "true"
	logoutCmd.Flags().BoolVar(&logoutAll, "all", false, "Remove every stored key and the token")
	logoutCmd.Flags().BoolVarP(&logoutYes, "yes", "y", false, "Do not ask for confirmation")
	whoamiCmd.Flags().BoolVar(&whoamiJSON, "json", false, "Print the access token's /api/user/me document as JSON")
}

// tokenSwitch is logout's --token. It shadows the global --token <pat>, so
// it also takes a token as its value (--token=dgpt_pat_…), to be removed if
// it is the stored one.
type tokenSwitch struct {
	on    bool
	value string
}

func (t *tokenSwitch) String() string   { return strconv.FormatBool(t.on) }
func (t *tokenSwitch) Type() string     { return "bool" }
func (t *tokenSwitch) IsBoolFlag() bool { return true }
func (t *tokenSwitch) Set(s string) (err error) {
	if strings.HasPrefix(s, config.TokenPrefix) {
		t.on, t.value = true, s
		return nil
	}
	t.on, err = strconv.ParseBool(s)
	return err
}

// readPipedSecret reads the first line of stdin.
func readPipedSecret(stdin io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(stdin, 4096)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	if s := strings.TrimSpace(line); s != "" {
		return s, nil
	}
	return "", usageErrf("nothing on stdin: pipe an agent API key or a personal access token")
}

// runLogin validates a personal access token against GET /api/user/me and
// stores it. A non-empty urlFlag (--url) is stored as the base URL too, since
// a token belongs to one deployment.
func runLogin(ctx context.Context, token, urlFlag string, out io.Writer) error {
	if !strings.HasPrefix(token, config.TokenPrefix) {
		return usageErrf("that does not look like a personal access token (expected it to start with %q); pipe an agent API key on stdin instead", config.TokenPrefix)
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
	return storeToken(&cfg, token, baseURL, id, out)
}

// storeToken saves a validated token together with the server that accepted
// it, wherever that URL came from (--url, DOCSGPT_URL or the config).
// Otherwise a later run without DOCSGPT_URL would send it to another server.
// Moving the stored agent keys to another server along with it needs the
// user's confirmation (saveKey has the reverse guard).
func storeToken(cfg *config.Config, token, baseURL string, id *manage.Identity, out io.Writer) error {
	baseURL = strings.TrimRight(baseURL, "/")
	if old := strings.TrimRight(cmp.Or(cfg.BaseURL, config.DefaultBaseURL), "/"); len(cfg.Keys) > 0 && old != baseURL {
		if !ui.Interactive() {
			return usageErrf("the stored agent keys (%s) belong to %s, and logging in to %s would send them there; remove them first (docsgpt-cli logout <name>), or set %s and %s instead of logging in",
				strings.Join(sortedNames(cfg.Keys), ", "), old, baseURL, config.EnvToken, config.EnvURL)
		}
		ok, err := ui.Confirm(fmt.Sprintf("Use %s from now on? Your agent keys would be sent there too.", hostOf(baseURL)), false)
		if err != nil {
			return err
		}
		if !ok {
			return &exitError{code: exitFailure, err: errors.New("aborted")}
		}
	}
	cfg.Token = token
	cfg.BaseURL = baseURL
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintln(out, display.Success("✓ Logged in.")+" Token stored in "+config.Path())
	printIdentity(out, id, token, config.TokenSourceConfig, baseURL)
	return nil
}

// loginKey checks a piped agent API key and stores it as the default key,
// named name, else after its agent.
func loginKey(ctx context.Context, key, name string, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return usageErrf("load config: %w", err)
	}
	baseURL := cfg.ResolveURL(globalURL)
	agent, verified, err := checkAgentKey(ctx, baseURL, key)
	if err != nil {
		return err
	}
	if !verified {
		warnLine(out, hostOf(baseURL)+" cannot check keys (no /v1/models); stored unverified")
	}
	if name == "" {
		name = storedKeyName(cfg, key)
	}
	if name == "" {
		name = freeKeyName(cfg, agent)
	}
	return saveKey(&cfg, name, key, baseURL, true, out)
}

// loginInteractive offers the stored keys to pick the default from, or adds
// a credential when there are none (or the user asks to).
func loginInteractive(ctx context.Context, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return usageErrf("load config: %w", err)
	}
	if len(cfg.Keys) > 0 {
		const add = "\x00add"
		host := hostOf(cfg.ResolveURL(globalURL))
		items, def := []ui.Item{}, 0
		for i, name := range sortedNames(cfg.Keys) {
			label := name
			if name == cfg.DefaultKey {
				label, def = name+" (default)", i
			}
			items = append(items, ui.Item{Label: label, Value: name, Description: config.RedactKey(cfg.Keys[name]) + " · " + host})
		}
		items = append(items, ui.Item{Label: "+ Add a key or token…", Value: add})
		name, err := ui.Select{
			Title: "API keys", Items: items, Default: def, Filter: true,
			Summary: func(it ui.Item) string {
				if it.Value == add {
					return ""
				}
				return "Default key: " + display.Accent(it.Value)
			},
		}.Run()
		if err != nil {
			return err
		}
		if name != add {
			cfg.DefaultKey = name
			return cfg.Save()
		}
	}
	_, err = addCredential(ctx, ui.Inline, &cfg, false, out)
	return err
}

// addCredential asks with p for an agent API key (or, unless keysOnly, a
// personal access token), checks it with the server behind a spinner and
// stores it. It returns the name the key was stored under ("" for a token).
func addCredential(ctx context.Context, p ui.Prompter, cfg *config.Config, keysOnly bool, out io.Writer) (string, error) {
	baseURL := cfg.ResolveURL(globalURL)
	title := "Paste an agent API key or a personal access token"
	if keysOnly {
		title = "Paste an agent API key"
	}
	var (
		agent    string
		verified bool
		id       *manage.Identity
	)
	secret, err := p.Input(ui.Input{
		Title: title,
		Mask:  true,
		Validate: func(vctx context.Context, v string) error {
			v = strings.TrimSpace(v)
			vctx, cancel := context.WithTimeout(vctx, 20*time.Second)
			defer cancel()
			var err error
			switch {
			case v == "":
				return errors.New("paste a key first")
			case strings.HasPrefix(v, config.TokenPrefix) && keysOnly:
				return errors.New("that is a personal access token; chatting needs an agent API key")
			case strings.HasPrefix(v, config.TokenPrefix):
				id, err = manage.New(baseURL, v, userAgent()).Me(vctx)
				if manage.IsUnauthorized(err) {
					return fmt.Errorf("%s rejected this token", hostOf(baseURL))
				}
			default:
				agent, verified, err = checkAgentKey(vctx, baseURL, v)
			}
			return err
		},
		Summary: func(v string) string {
			v = strings.TrimSpace(v)
			switch {
			case id != nil:
				return "Access token " + config.RedactToken(v)
			case agent != "":
				return "API key " + config.RedactKey(v) + display.Muted(" · agent "+agent)
			case !verified:
				return "API key " + config.RedactKey(v) + display.Muted(" · not verified: "+hostOf(baseURL)+" has no /v1/models")
			}
			return "API key " + config.RedactKey(v)
		},
	})
	if err != nil {
		return "", err
	}
	secret = strings.TrimSpace(secret)
	if id != nil {
		return "", storeToken(cfg, secret, baseURL, id, out)
	}

	name := storedKeyName(*cfg, secret)
	if name == "" {
		name, err = p.Input(ui.Input{
			Title: "Name for this key",
			Value: freeKeyName(*cfg, agent),
			Validate: func(_ context.Context, v string) error {
				v = strings.TrimSpace(v)
				if v == "" {
					return errors.New("enter a name")
				}
				if _, taken := cfg.Keys[v]; taken {
					return fmt.Errorf("another key is already named %q", v)
				}
				return nil
			},
		})
		if err != nil {
			return "", err
		}
		name = strings.TrimSpace(name)
	}
	makeDefault := len(cfg.Keys) == 0 || cfg.DefaultKey == "" || cfg.DefaultKey == name
	if !makeDefault {
		if makeDefault, err = ui.ConfirmWith(p, "Make it the default?", true); err != nil {
			return "", err
		}
	}
	return name, saveKey(cfg, name, secret, baseURL, makeDefault, out)
}

// checkAgentKey asks the server which agent a key belongs to (GET
// /v1/models), which runs nothing and spends no tokens. A server without
// that route cannot check keys: verified is then false and the key is taken
// on trust.
func checkAgentKey(ctx context.Context, baseURL, key string) (agent string, verified bool, err error) {
	models, err := docsgpt.NewClient(baseURL, key).Models(ctx)
	var apiErr *docsgpt.APIError
	var urlErr *url.Error
	switch {
	case errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden):
		return "", false, fmt.Errorf("%s rejected this key", hostOf(baseURL))
	case errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed):
		return "", false, nil
	case errors.As(err, &apiErr):
		return "", false, fmt.Errorf("%s answered %d", hostOf(baseURL), apiErr.StatusCode)
	case errors.As(err, &urlErr):
		return "", false, fmt.Errorf("could not reach %s: %w", hostOf(baseURL), urlErr.Err)
	case err != nil:
		return "", false, err
	}
	if len(models) > 0 {
		agent = models[0].Name
	}
	return agent, true, nil
}

// saveKey stores an agent key. It also stores the base URL that accepted it,
// unless that would point a stored access token at another server.
func saveKey(cfg *config.Config, name, key, baseURL string, makeDefault bool, out io.Writer) error {
	cfg.Keys[name] = key
	if makeDefault {
		cfg.DefaultKey = name
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if cfg.Token == "" || strings.TrimRight(cfg.BaseURL, "/") == baseURL {
		cfg.BaseURL = baseURL
	} else {
		warnLine(out, fmt.Sprintf("the server stays %s (the stored access token belongs to it); chat with this key using --url %s", cfg.BaseURL, baseURL))
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	msg := fmt.Sprintf("Saved key %q", name)
	if cfg.DefaultKey == name {
		msg += " as the default"
	}
	fmt.Fprintln(out, display.Success("✓ "+msg+"."))
	return nil
}

// storedKeyName returns the name key is already stored under, if any.
func storedKeyName(cfg config.Config, key string) string {
	for _, name := range sortedNames(cfg.Keys) {
		if cfg.Keys[name] == key {
			return name
		}
	}
	return ""
}

// freeKeyName suggests an unused key name from the agent's name.
func freeKeyName(cfg config.Config, agent string) string {
	base := strings.Trim(strings.Join(strings.FieldsFunc(strings.ToLower(agent), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
	}), "-"), "-")
	if base == "" {
		base = "default"
	}
	name := base
	for i := 2; ; i++ {
		if _, taken := cfg.Keys[name]; !taken {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

func sortedNames(keys map[string]string) []string {
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// hostOf shortens a base URL to its host for messages.
func hostOf(baseURL string) string {
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return baseURL
}

// chatKey resolves the agent key of ask and chat. With none configured, a
// terminal session asks for one inline and carries on with it.
func chatKey(cfg *config.Config) (name, key string, err error) {
	name, key, err = cfg.ResolveKey(globalKey)
	if !errors.Is(err, config.ErrNoKey) {
		return name, key, err
	}
	if !ui.Interactive() {
		return "", "", fmt.Errorf("No API key. Run 'docsgpt-cli login' or set %s.", config.EnvAPIKey)
	}
	fmt.Fprintln(os.Stderr, display.Muted("No API key yet — paste one from DocsGPT → Agent settings → API key"))
	if name, err = addCredential(context.Background(), ui.Inline, cfg, true, os.Stderr); err != nil {
		return "", "", err
	}
	fmt.Fprintln(os.Stderr)
	return name, cfg.Keys[name], nil
}

func runLogout(args []string, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return usageErrf("load config: %w", err)
	}
	// A token, given alone or after --token (which then takes no value),
	// names the stored token: key names never look like one.
	byToken := logoutToken
	if len(args) == 1 && strings.HasPrefix(args[0], config.TokenPrefix) {
		byToken, args = tokenSwitch{on: true, value: args[0]}, nil
	}
	if byToken.value != "" && byToken.value != cfg.Token {
		stored := "none is stored"
		if cfg.Token != "" {
			stored = "the stored one is " + config.RedactToken(cfg.Token)
		}
		return usageErrf("%s is not the stored access token (%s)", config.RedactToken(byToken.value), stored)
	}
	var keys []string
	token := false
	switch {
	case logoutAll:
		if len(args) > 0 || byToken.on {
			return usageErrf("--all removes everything; drop the other arguments")
		}
		keys, token = sortedNames(cfg.Keys), cfg.Token != ""
	case len(args) == 1 || byToken.on:
		if len(args) == 1 {
			if _, ok := cfg.Keys[args[0]]; !ok {
				return usageErrf("no stored key named %q (stored: %s)", args[0], textOrDash(strings.Join(sortedNames(cfg.Keys), ", ")))
			}
			keys = args
		}
		token = byToken.on && cfg.Token != ""
		if byToken.on && cfg.Token == "" {
			fmt.Fprintln(out, "No stored token.")
		}
	case ui.Interactive():
		host := hostOf(cfg.ResolveURL(globalURL))
		var items []ui.Item
		for _, name := range sortedNames(cfg.Keys) {
			label := name
			if name == cfg.DefaultKey {
				label += " (default)"
			}
			items = append(items, ui.Item{Label: label, Value: "key:" + name, Description: config.RedactKey(cfg.Keys[name]) + " · " + host})
		}
		if cfg.Token != "" {
			items = append(items, ui.Item{Label: "Access token", Value: "token", Description: config.RedactToken(cfg.Token) + " · " + host})
		}
		if len(items) == 0 {
			fmt.Fprintln(out, "Nothing stored.")
			return nil
		}
		v, err := ui.Select{Title: "Remove which credential?", Items: items, Filter: true, Summary: func(ui.Item) string { return "" }}.Run()
		if err != nil {
			return err
		}
		if name, ok := strings.CutPrefix(v, "key:"); ok {
			keys = []string{name}
		} else {
			token = true
		}
	default:
		return usageErrf("say what to remove: logout <name>, --token or --all")
	}
	if len(keys) == 0 && !token {
		return nil
	}

	var what []string
	for _, k := range keys {
		what = append(what, fmt.Sprintf("key %q", k))
	}
	if token {
		what = append(what, "the access token")
	}
	question := "Remove " + strings.Join(what, ", ") + "?"
	if !logoutYes {
		if err := confirmDestructive(question); err != nil {
			return err
		}
	}

	for _, k := range keys {
		delete(cfg.Keys, k)
	}
	oldDefault := cfg.DefaultKey
	if _, ok := cfg.Keys[cfg.DefaultKey]; !ok {
		cfg.DefaultKey = ""
		if names := sortedNames(cfg.Keys); len(names) > 0 {
			cfg.DefaultKey = names[0]
		}
	}
	if token {
		cfg.Token = ""
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintln(out, display.Success("✓ Removed "+strings.Join(what, ", ")+".")+
		display.Muted(" Only forgotten here; revoke in DocsGPT to invalidate."))
	if cfg.DefaultKey != oldDefault && cfg.DefaultKey != "" {
		fmt.Fprintln(out, "The default key is now "+cfg.DefaultKey+".")
	}
	if token && os.Getenv(config.EnvToken) != "" {
		warnLine(out, config.EnvToken+" is still set in this environment and will keep being used")
	}
	if len(keys) > 0 && os.Getenv(config.EnvAPIKey) != "" {
		warnLine(out, config.EnvAPIKey+" is still set in this environment and will keep being used")
	}
	return nil
}

func runWhoami(ctx context.Context, asJSON bool, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return usageErrf("load config: %w", err)
	}
	baseURL := cfg.ResolveURL(globalURL)
	token, source := cfg.ResolveToken(globalToken)
	// Not being logged in is an answer, not a usage error: exit 1.
	if asJSON {
		if token == "" {
			return fmt.Errorf("no personal access token configured: run 'docsgpt-cli login', set %s, or pass --token", config.EnvToken)
		}
		id, err := manage.New(baseURL, token, userAgent()).Me(ctx)
		if err != nil {
			return err
		}
		return writeJSON(out, id.Raw)
	}

	name, key, keyErr := cfg.ResolveKey(globalKey)
	if keyErr != nil && !errors.Is(keyErr, config.ErrNoKey) {
		return usageErr(keyErr)
	}
	if key == "" && token == "" {
		return fmt.Errorf("not logged in: run 'docsgpt-cli login', or set %s / %s", config.EnvAPIKey, config.EnvToken)
	}
	row := func(k, v string) { fmt.Fprintf(out, "  %-13s %s\n", k, v) }
	var failed error
	if key == "" {
		fmt.Fprintln(out, "Agent API key: none (run 'docsgpt-cli login')")
	} else {
		fmt.Fprintln(out, "Agent API key")
		switch {
		case name == config.EnvAPIKey:
			name = "from " + config.EnvAPIKey
		case globalKey != "":
			name += " (--key)"
		default:
			name += " (default)"
		}
		row("Name", name)
		row("Key", config.RedactKey(key))
		row("Server", baseURL)
		agent, verified, err := checkAgentKey(ctx, baseURL, key)
		switch {
		case err != nil:
			row("Agent", display.Danger("✗ "+err.Error()))
			failed = &exitError{code: exitFailure, err: err}
		case !verified:
			row("Agent", "unknown (the server cannot check keys)")
		default:
			row("Agent", textOrDash(agent))
		}
	}
	fmt.Fprintln(out)
	if token == "" {
		fmt.Fprintln(out, "Personal access token: none (needed for agents, sources and bench agent_id runs)")
		return failed
	}
	fmt.Fprintln(out, "Personal access token")
	id, err := manage.New(baseURL, token, userAgent()).Me(ctx)
	if err != nil {
		return err
	}
	printIdentity(out, id, token, source, baseURL)
	return failed
}

// printIdentity renders the /api/user/me document. The token is only ever
// shown redacted.
func printIdentity(out io.Writer, id *manage.Identity, token, source, baseURL string) {
	row := func(k, v string) { fmt.Fprintf(out, "  %-13s %s\n", k, display.Safe(v)) }
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
