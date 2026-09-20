package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"docsgpt-cli/internal/display"
	"docsgpt-cli/internal/manage"

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
	Short: "List, upload and delete sources (personal access token)",
	Long: `Manage knowledge sources with a personal access token.

  docsgpt-cli sources list
  docsgpt-cli sources upload docs/*.md --name "Product docs" --wait
  docsgpt-cli sources delete <id> --yes

Scopes: sources:read (list) and sources:write (upload incl. --wait, delete).`,
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

var promptsCmd = &cobra.Command{
	Use:   "prompts",
	Short: "List prompts (personal access token)",
}

var promptsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List built-in, private and team prompts (scope prompts:read)",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runPromptsList(ctx, c, promptsListJSON, os.Stdout)
		})
	},
}

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "List configured tools (personal access token)",
}

var toolsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your configured tools (scope tools:read)",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runToolsList(ctx, c, toolsListJSON, os.Stdout)
		})
	},
}

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

	promptsListCmd.Flags().BoolVar(&promptsListJSON, "json", false, "Print the server's prompt list as JSON")
	toolsListCmd.Flags().BoolVar(&toolsListJSON, "json", false, "Print the server's tool list as JSON")

	sourcesCmd.AddCommand(sourcesListCmd, sourcesUploadCmd, sourcesDeleteCmd)
	promptsCmd.AddCommand(promptsListCmd)
	toolsCmd.AddCommand(toolsListCmd)
	markManagement(sourcesCmd)
	markManagement(promptsCmd)
	markManagement(toolsCmd)
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
	// Replace deletes the caller's older sources with the same name once the
	// new one is ingested, so agents that reference the source by name pick
	// up the new content on their next apply.
	Replace bool

	// Poll tunes the --wait backoff; zero values use the client defaults.
	Poll manage.WaitOptions
}

// uploadReport is the --json document of `sources upload`.
type uploadReport struct {
	Name           string   `json:"name"`
	TaskID         string   `json:"task_id"`
	SourceID       string   `json:"source_id,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	Deduplicated   bool     `json:"deduplicated,omitempty"`
	Waited         bool     `json:"waited"`
	Status         string   `json:"status,omitempty"`
	Replaced       []string `json:"replaced,omitempty"`
	Error          string   `json:"error,omitempty"`
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

	fmt.Fprintf(stderr, "uploading %d file(s) as %q...\n", len(opts.Files), opts.Name)
	res, err := c.UploadSource(ctx, opts.Name, opts.Files, key)
	if err != nil {
		return err
	}
	report := uploadReport{Name: opts.Name, TaskID: res.TaskID, SourceID: res.SourceID, IdempotencyKey: key}

	finish := func(runErr error) error {
		if runErr != nil {
			report.Error = runErr.Error()
		}
		if opts.JSON {
			if err := writeJSON(stdout, report); err != nil && runErr == nil {
				return err
			}
			return runErr
		}
		if runErr == nil {
			fmt.Fprintf(stdout, "%s source %s (task %s", display.Success("ok"), textOrDash(report.SourceID), report.TaskID)
			if report.Status != "" {
				fmt.Fprintf(stdout, ", %s", report.Status)
			}
			fmt.Fprintln(stdout, ")")
		}
		return runErr
	}

	if res.TaskID == manage.DeduplicatedTaskID {
		// The key matched an earlier request whose task record is gone: that
		// ingest already ran, there is nothing to poll.
		report.Deduplicated = true
		fmt.Fprintln(stderr, "the server deduplicated this upload (same Idempotency-Key as an earlier request); nothing new was ingested")
		return finish(replaceOlderSources(ctx, c, opts, &report, stderr))
	}
	if !opts.Wait {
		fmt.Fprintf(stderr, "ingestion queued; poll it with --wait or check the web app (task %s)\n", res.TaskID)
		return finish(nil)
	}

	waitCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	poll := opts.Poll
	started := time.Now()
	lastLine := ""
	poll.OnUpdate = func(status string, task *manage.TaskStatus) {
		line := status
		if task != nil {
			if pct, ok := task.Progress(); ok && status != "SUCCESS" {
				line = fmt.Sprintf("%s %d%%", status, pct)
			}
		}
		if line == lastLine {
			return
		}
		lastLine = line
		fmt.Fprintf(stderr, "  [%4.0fs] %s\n", time.Since(started).Seconds(), line)
	}
	report.Waited = true
	st, err := c.WaitTask(waitCtx, res.TaskID, poll)
	if st != nil {
		report.Status = st.Status
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			err = fmt.Errorf("timed out after %s waiting for ingestion (task %s is still running server-side; re-run with the same files to resume waiting): %w", opts.Timeout, res.TaskID, err)
		}
		return finish(err)
	}
	return finish(replaceOlderSources(ctx, c, opts, &report, stderr))
}

// replaceOlderSources implements --replace. The server resolves an agent's
// source by name and picks the oldest match, so without this every upload of
// changed content leaves agents pinned to the first upload. Only the caller's
// own sources are touched, never the one just uploaded, and nothing is deleted
// unless the server said which source that is.
func replaceOlderSources(ctx context.Context, c *manage.Client, opts uploadOptions, report *uploadReport, stderr io.Writer) error {
	if !opts.Replace {
		return nil
	}
	if report.SourceID == "" {
		fmt.Fprintln(stderr, "--replace skipped: the server did not report the new source id")
		return nil
	}
	sources, _, err := c.ListSources(ctx)
	if err != nil {
		return fmt.Errorf("--replace: list sources: %w", err)
	}
	// The id the server reported must be a source that exists right now. After a
	// revert to earlier content the Idempotency-Key repeats and, within the
	// server's dedup window, the cached reply names the source of that earlier
	// upload, which a later --replace may already have deleted. Deleting "every
	// other" source would then remove the only live one.
	live := false
	for _, src := range sources {
		if src.ID == report.SourceID {
			live = true
			break
		}
	}
	if !live {
		return fmt.Errorf("--replace: the server reported source %s, which no longer exists (this content was uploaded before and the request was deduplicated); nothing was deleted. Re-run with a fresh --idempotency-key to ingest it again", report.SourceID)
	}
	for _, src := range sources {
		if src.ID == report.SourceID || !strings.EqualFold(src.Name, opts.Name) {
			continue
		}
		if src.Ownership != "" && src.Ownership != "user" {
			continue
		}
		if err := c.DeleteSource(ctx, src.ID); err != nil {
			return fmt.Errorf("--replace: delete older source %s: %w", src.ID, err)
		}
		report.Replaced = append(report.Replaced, src.ID)
		fmt.Fprintf(stderr, "replaced older source %s\n", src.ID)
	}
	return nil
}
