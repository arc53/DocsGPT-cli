package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
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
	sourcesUploadSync    bool
	sourcesUploadForce   bool
	sourcesUploadQuiet   bool
	sourcesUploadInclude []string
	sourcesUploadExclude []string
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
	Example: `  docsgpt-cli sources list
  docsgpt-cli sources list --json | jq -r '.[] | select(.name == "Product docs") | .id'`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runSourcesList(ctx, c, sourcesListJSON, os.Stdout)
		})
	},
}

var sourcesUploadCmd = &cobra.Command{
	Use:   "upload <file|folder>...",
	Short: "Upload files or a folder as a source; --sync keeps it up to date",
	Long: `Upload files (documents, or .zip archives) or folders as a source named
--name. Ingestion runs asynchronously on the server; --wait polls the task
until it succeeds or fails and makes the exit code reflect the outcome.

A folder is uploaded with its tree: every file under it the server can parse,
under its path inside the folder. Hidden files and folders, empty files and
other file types are left out; --exclude and --include narrow it further.

--sync updates your source named --name in place instead of adding a new
one: the files become its whole file set, files missing from them are
removed, and only added, edited and removed files are re-embedded. Only the
files the source lacks are uploaded, and when nothing changed since the last
sync nothing is sent at all, so it suits a scheduled job:

  */15 * * * *  docsgpt-cli sources upload ~/docs --name "Docs" --sync --wait --quiet

The source keeps its id, so agents need no re-apply. With no source of that
name yet, it is created as usual.

Retries are safe: every upload carries an Idempotency-Key. By default it is
derived from --name and the SHA-256 of each file, so re-running the same upload
(a retried CI job) returns the original task instead of ingesting twice, while
any change to the name or the content produces a new key. The server remembers
keys for about 24 hours; to force a fresh ingest of identical content within
that window pass your own --idempotency-key, or --idempotency-key "" to send
none.

Exit codes: 0 ok, 1 upload/ingestion failed or timed out, 2 usage error.`,
	Example: `  docsgpt-cli sources upload docs/*.md --name "Product docs" --wait
  docsgpt-cli sources upload ./docs --name "Product docs" --sync --wait
  docsgpt-cli sources upload ./docs --name "Product docs" --sync --wait --exclude drafts --exclude '*.png'`,
	Args: usageArgs(cobra.MinimumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runSourcesUpload(ctx, c, uploadOptions{
				Files:    args,
				Name:     sourcesUploadName,
				Wait:     sourcesUploadWait,
				Timeout:  sourcesUploadTO,
				Key:      sourcesUploadKey,
				KeySet:   cmd.Flags().Changed("idempotency-key"),
				JSON:     sourcesUploadJSON,
				Replace:  sourcesUploadReplace,
				Sync:     sourcesUploadSync,
				Force:    sourcesUploadForce,
				Quiet:    sourcesUploadQuiet,
				Include:  sourcesUploadInclude,
				Exclude:  sourcesUploadExclude,
				StateDir: filepath.Join(config.Dir(), "sync"),
			}, os.Stdout, os.Stderr)
		})
	},
}

var sourcesDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a source and its index",
	Long:  "Delete a source and its index. It asks first; without a terminal it needs --yes.",
	Example: `  docsgpt-cli sources delete <id>
  docsgpt-cli sources delete <id> --yes   # in CI`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			if !sourcesDeleteYes {
				if err := confirmDestructive("Delete source " + args[0] + "?"); err != nil {
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
	uf.BoolVar(&sourcesUploadSync, "sync", false, "Update your source named --name in place: upload and re-embed only changed files (created if missing)")
	uf.BoolVar(&sourcesUploadForce, "force", false, "With --sync: ask the server even when nothing changed since the last sync")
	uf.BoolVarP(&sourcesUploadQuiet, "quiet", "q", false, "Print no progress, only the result and errors (for scheduled jobs)")
	uf.StringArrayVar(&sourcesUploadExclude, "exclude", nil, "Leave out a folder's files or folders matching this glob (repeatable), e.g. drafts, '*.png'")
	uf.StringArrayVar(&sourcesUploadInclude, "include", nil, "Upload only a folder's files matching this glob (repeatable), e.g. '*.md'")
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", textOrDash(s.ID), textOrDash(s.Name), textOrDash(s.Type),
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
		fmt.Fprintf(tw, "%s\t%s\t%s\n", textOrDash(p.ID), textOrDash(p.Name), textOrDash(p.Type))
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\n", textOrDash(t.ID), textOrDash(t.Name), textOrDash(name), t.Status, kind)
	}
	return tw.Flush()
}

// uploadOptions are the inputs of `sources upload`.
type uploadOptions struct {
	Files   []string // files and folders
	Name    string
	Wait    bool
	Timeout time.Duration
	Key     string
	KeySet  bool // --idempotency-key was passed explicitly ("" disables the header)
	JSON    bool
	Replace bool
	Sync    bool
	Force   bool     // ask the server even when the local record says nothing changed
	Quiet   bool     // no progress lines on stderr: only the result, and errors
	Include []string // of a folder: only files matching one of these
	Exclude []string // of a folder: files and folders to leave out

	// StateDir holds the record of each source's last sync; "" keeps none.
	StateDir string
	// Poll tunes the --wait backoff; zero values use the client defaults.
	Poll manage.WaitOptions
}

// runSourcesUpload uploads the files and, with Wait, polls the ingest task.
// Progress goes to stderr; stdout carries the result (text or JSON).
func runSourcesUpload(ctx context.Context, c *manage.Client, opts uploadOptions, stdout, stderr io.Writer) error {
	if opts.Quiet {
		stderr = io.Discard
	}
	if opts.Name == "" {
		return usageErrf("--name is required")
	}
	if opts.Sync && opts.Replace {
		return usageErrf("--sync and --replace cannot be combined: --sync keeps one source and updates it")
	}
	if opts.Wait && opts.Timeout <= 0 {
		return usageErrf("--timeout must be positive")
	}
	if opts.Replace && !opts.Wait {
		return usageErrf("--replace needs --wait: older sources are only deleted once the new one is ingested")
	}
	if opts.Force && !opts.Sync {
		return usageErrf("--force only applies to --sync")
	}
	for _, f := range opts.Files {
		if _, err := os.Stat(f); errors.Is(err, fs.ErrNotExist) {
			return usageErrf("%s: no such file", f)
		} else if err != nil {
			return usageErr(err)
		}
	}
	found, err := manage.CollectFiles(opts.Files, manage.CollectOptions{Include: opts.Include, Exclude: opts.Exclude})
	if err != nil {
		return usageErr(err)
	}
	if (len(opts.Include) > 0 || len(opts.Exclude) > 0) && !found.Folders {
		return usageErrf("--include and --exclude choose among a folder's files; pass a folder")
	}
	reportSkipped(stderr, found.Skipped)
	if len(found.Files) == 0 {
		return usageErrf("no files to upload: the folder holds none the server can parse")
	}
	if found.Folders && found.Archives {
		return usageErrf("a .zip archive and a folder cannot be uploaded together: the server unpacks the archive over the folder's files")
	}
	// A manifest names every file once and cannot say what an archive
	// holds: those syncs send every file, as they always did.
	whole := found.Archives || found.Duplicate != ""
	if opts.Sync && !whole {
		if err := manage.HashFiles(found.Files); err != nil {
			return err
		}
	}

	// A sync carries no derived key: syncing the same files again is
	// harmless, while a content-derived key would make a revert within the
	// server's dedup window replay the earlier sync instead of writing.
	key, syncKey := opts.Key, opts.Key
	if !opts.KeySet {
		derived, err := manage.DeriveIdempotencyKey(opts.Name, found.Files)
		if err != nil {
			return err
		}
		key = derived
	}
	if len(key) > manage.IdempotencyKeyMaxLen {
		return usageErrf("--idempotency-key exceeds %d characters", manage.IdempotencyKeyMaxLen)
	}

	report, err := c.Ingest(ctx, manage.IngestOptions{
		Name: opts.Name, Files: found.Files, Key: key, SyncKey: syncKey, Wait: opts.Wait, Timeout: opts.Timeout,
		Replace: opts.Replace, Sync: opts.Sync, Whole: whole, StateDir: opts.StateDir, Force: opts.Force,
		Poll: opts.Poll, Log: stderr,
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
		if report.Unchanged {
			fmt.Fprintf(stdout, "%s source %s (unchanged, %d files)\n", display.Success("ok"), textOrDash(report.SourceID), report.Files)
			return nil
		}
		fmt.Fprintf(stdout, "%s source %s (task %s", display.Success("ok"), textOrDash(report.SourceID), textOrDash(report.TaskID))
		if report.Status != "" {
			fmt.Fprintf(stdout, ", %s", display.Safe(report.Status))
		}
		if report.Synced {
			fmt.Fprintf(stdout, ", %d of %d files uploaded", report.Uploaded, report.Files)
		}
		fmt.Fprintln(stdout, ")")
	}
	return err
}

// reportSkipped says which of a folder's files are not uploaded: a count
// per reason, with the first few paths.
func reportSkipped(w io.Writer, skipped []manage.SkippedFile) {
	if len(skipped) == 0 {
		return
	}
	byReason := map[string][]string{}
	for _, s := range skipped {
		byReason[s.Reason] = append(byReason[s.Reason], s.Path)
	}
	reasons := make([]string, 0, len(byReason))
	for reason := range byReason {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	const show = 3
	for _, reason := range reasons {
		paths := byReason[reason]
		shown := paths
		more := ""
		if len(shown) > show {
			shown, more = shown[:show], fmt.Sprintf(", and %d more", len(paths)-show)
		}
		for i, p := range shown {
			shown[i] = display.Safe(p)
		}
		fmt.Fprintf(w, "skipped %d file(s), %s: %s%s\n", len(paths), reason, strings.Join(shown, ", "), more)
	}
}
