package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/manage"

	"github.com/spf13/cobra"
)

var (
	sourcesListJSON      bool
	sourcesUploadName    string
	sourcesUploadWait    bool
	sourcesUploadTO      time.Duration
	sourcesUploadKey     string
	sourcesUploadJSON    bool
	sourcesUploadReplace bool
	sourcesDeleteYes     bool
	promptsListJSON      bool
	toolsListJSON        bool
)

var sourcesCmd = &cobra.Command{
	Use:   "sources",
	Short: "List, upload and delete sources (access token)",
	Long: `Manage the knowledge sources agents answer from. Needs a personal access
token (see 'docsgpt-cli login') with sources:read to list and sources:write to
upload or delete.`,
	Example: `  docsgpt-cli sources list
  docsgpt-cli sources upload docs/*.md --name "Product docs" --wait
  docsgpt-cli sources delete <id> --yes`,
}

var sourcesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your sources",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runSourcesList(ctx, c, sourcesListJSON, os.Stdout)
		})
	},
}

var sourcesUploadCmd = &cobra.Command{
	Use:   "upload <file>...",
	Short: "Upload files as a new source and (optionally) wait for ingestion",
	Long: `Upload one or more files (documents, or .zip archives) as a source named
--name. Ingestion runs asynchronously on the server; --wait polls the task
until it succeeds or fails and makes the exit code reflect the outcome.

Retries are safe: every upload carries an Idempotency-Key. By default it is
derived from --name and the SHA-256 of each file, so re-running the same upload
(a retried CI job) returns the original task instead of ingesting twice, while
any change to the name or the content produces a new key. The server remembers
keys for about 24 hours; to force a fresh ingest of identical content within
that window pass your own --idempotency-key, or --idempotency-key "" to send
none.

Exit codes: 0 ok, 1 upload/ingestion failed or timed out, 2 usage error.`,
	Args: usageArgs(cobra.MinimumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runSourcesUpload(ctx, c, uploadOptions{
				Files:   args,
				Name:    sourcesUploadName,
				Wait:    sourcesUploadWait,
				Timeout: sourcesUploadTO,
				Key:     sourcesUploadKey,
				KeySet:  cmd.Flags().Changed("idempotency-key"),
				JSON:    sourcesUploadJSON,
				Replace: sourcesUploadReplace,
			}, os.Stdout, os.Stderr)
		})
	},
}

var sourcesDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a source and its index",
	Args:  usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			if !sourcesDeleteYes {
				if err := confirmDestructive(os.Stdin, os.Stderr, stdinIsTerminal(), "Delete source "+args[0]+"?"); err != nil {
					return err
				}
			}
			if err := c.DeleteSource(ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, display.Success("deleted"), "source", args[0])
			return nil
		})
	},
}

// agents prompts / agents tools list what agent YAML can reference.
var agentsPromptsCmd = &cobra.Command{
	Use:   "prompts",
	Short: "List the prompts agents can use (scope prompts:read)",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runPromptsList(ctx, c, promptsListJSON, os.Stdout)
		})
	},
}

var agentsToolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "List your configured tools (scope tools:read)",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runToolsList(ctx, c, toolsListJSON, os.Stdout)
		})
	},
}

// The old paths, `prompts list` and `tools list`.
var (
	promptsCmd     = &cobra.Command{Use: "prompts", Hidden: true}
	promptsListCmd = &cobra.Command{Use: "list", Args: agentsPromptsCmd.Args, RunE: agentsPromptsCmd.RunE}
	toolsCmd       = &cobra.Command{Use: "tools", Hidden: true}
	toolsListCmd   = &cobra.Command{Use: "list", Args: agentsToolsCmd.Args, RunE: agentsToolsCmd.RunE}
)

func init() {
	sourcesListCmd.Flags().BoolVar(&sourcesListJSON, "json", false, "Print the server's source list as JSON")

	uf := sourcesUploadCmd.Flags()
	uf.StringVar(&sourcesUploadName, "name", "", "Name of the new source (required)")
	uf.BoolVar(&sourcesUploadWait, "wait", false, "Wait for ingestion to finish; exit non-zero if it fails")
	uf.DurationVar(&sourcesUploadTO, "timeout", 10*time.Minute, "How long --wait polls before giving up (e.g. 90s, 15m)")
	uf.StringVar(&sourcesUploadKey, "idempotency-key", "", "Idempotency-Key header (default: derived from --name and file hashes; \"\" sends none)")
	uf.BoolVar(&sourcesUploadReplace, "replace", false, "After ingestion, delete your older sources with the same name (needs --wait); re-apply agents that use it")
	uf.BoolVar(&sourcesUploadJSON, "json", false, "Print the result as JSON on stdout")

	sourcesDeleteCmd.Flags().BoolVarP(&sourcesDeleteYes, "yes", "y", false, "Do not ask for confirmation")

	for _, c := range []*cobra.Command{agentsPromptsCmd, promptsListCmd} {
		c.Flags().BoolVar(&promptsListJSON, "json", false, "Print the server's prompt list as JSON")
	}
	for _, c := range []*cobra.Command{agentsToolsCmd, toolsListCmd} {
		c.Flags().BoolVar(&toolsListJSON, "json", false, "Print the server's tool list as JSON")
	}

	sourcesCmd.AddCommand(sourcesListCmd, sourcesUploadCmd, sourcesDeleteCmd)
	promptsCmd.AddCommand(promptsListCmd)
	toolsCmd.AddCommand(toolsListCmd)
	groupCommand(sourcesCmd)
}

func runSourcesList(ctx context.Context, c *manage.Client, asJSON bool, out io.Writer) error {
	sources, raw, err := c.ListSources(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, raw)
	}
	if len(sources) == 0 {
		fmt.Fprintln(out, "No sources.")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTYPE\tTOKENS\tDATE\tOWNERSHIP")
	for _, s := range sources {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, textOrDash(s.Name), textOrDash(s.Type),
			textOrDash(anyText(s.Tokens)), textOrDash(anyText(s.Date)), textOrDash(s.Ownership))
	}
	return tw.Flush()
}

func runPromptsList(ctx context.Context, c *manage.Client, asJSON bool, out io.Writer) error {
	prompts, raw, err := c.ListPrompts(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, raw)
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTYPE")
	for _, p := range prompts {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", p.ID, textOrDash(p.Name), textOrDash(p.Type))
	}
	return tw.Flush()
}

func runToolsList(ctx context.Context, c *manage.Client, asJSON bool, out io.Writer) error {
	tools, raw, err := c.ListTools(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		if len(raw) == 0 {
			raw = []byte("[]")
		}
		return writeJSON(out, raw)
	}
	if len(tools) == 0 {
		fmt.Fprintln(out, "No tools.")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tNAME\tENABLED\tKIND")
	for _, t := range tools {
		kind := "user"
		switch {
		case t.Builtin:
			kind = "builtin"
		case t.Default:
			kind = "default"
		case t.Ownership == "team":
			kind = "team"
		}
		name := t.CustomName
		if name == "" {
			name = t.DisplayName
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\n", t.ID, textOrDash(t.Name), textOrDash(name), t.Status, kind)
	}
	return tw.Flush()
}

// uploadOptions are the inputs of `sources upload`.
type uploadOptions struct {
	Files   []string
	Name    string
	Wait    bool
	Timeout time.Duration
	Key     string
	KeySet  bool // --idempotency-key was passed explicitly ("" disables the header)
	JSON    bool
	Replace bool

	// Poll tunes the --wait backoff; zero values use the client defaults.
	Poll manage.WaitOptions
}

// runSourcesUpload uploads the files and, with Wait, polls the ingest task.
// Progress goes to stderr; stdout carries the result (text or JSON).
func runSourcesUpload(ctx context.Context, c *manage.Client, opts uploadOptions, stdout, stderr io.Writer) error {
	if opts.Name == "" {
		return usageErrf("--name is required")
	}
	if opts.Wait && opts.Timeout <= 0 {
		return usageErrf("--timeout must be positive")
	}
	if opts.Replace && !opts.Wait {
		return usageErrf("--replace needs --wait: older sources are only deleted once the new one is ingested")
	}
	for _, f := range opts.Files {
		st, err := os.Stat(f)
		if err != nil {
			return usageErr(err)
		}
		if st.IsDir() {
			return usageErrf("%s is a directory: pass files, or a .zip archive of it", f)
		}
	}

	key := opts.Key
	if !opts.KeySet {
		derived, err := manage.DeriveIdempotencyKey(opts.Name, opts.Files)
		if err != nil {
			return err
		}
		key = derived
	}
	if len(key) > manage.IdempotencyKeyMaxLen {
		return usageErrf("--idempotency-key exceeds %d characters", manage.IdempotencyKeyMaxLen)
	}

	report, err := c.Ingest(ctx, manage.IngestOptions{
		Name: opts.Name, Files: opts.Files, Key: key, Wait: opts.Wait, Timeout: opts.Timeout,
		Replace: opts.Replace, Poll: opts.Poll, Log: stderr,
	})
	if report == nil {
		return err
	}
	if opts.JSON {
		if jsonErr := writeJSON(stdout, report); jsonErr != nil && err == nil {
			return jsonErr
		}
		return err
	}
	if err == nil {
		fmt.Fprintf(stdout, "%s source %s (task %s", display.Success("ok"), textOrDash(report.SourceID), report.TaskID)
		if report.Status != "" {
			fmt.Fprintf(stdout, ", %s", report.Status)
		}
		fmt.Fprintln(stdout, ")")
	}
	return err
}
