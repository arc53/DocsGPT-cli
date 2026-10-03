package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/manage"

	"github.com/spf13/cobra"
)

var (
	agentsListJSON   bool
	agentsExportOut  string
	agentsFiles      []string
	agentsResolve    []string
	agentsDryRun     bool
	agentsApplyJSON  bool
	agentsDeleteYes  bool
	agentsPlanFiles  []string
	agentsPlanJSON   bool
	agentsPlanResolv []string

	agentsTriggerWebhook string
	agentsTriggerFile    string
	agentsTriggerKey     string
	agentsTriggerWait    bool
	agentsTriggerTO      time.Duration
	agentsTriggerJSON    bool
)

const resolveHelp = `A reference the server cannot match is settled with --resolve (repeatable):

  source:<name>=<source-id>|skip
  tool:<sel>=reuse:<tool-id>|create|skip
  tool:<sel>.secret.<field>=<value>
  model:<name>=<api-key>|skip

<sel> is tool-N (its position in spec.tools) or the tool's name or type.`

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "Manage agents as code: list, export, apply (access token)",
	Long: `Manage agents as YAML you can keep in git. Needs a personal access token
(see 'docsgpt-cli login') with agents:read to list and export, agents:write to
plan, apply and delete, and agents:keys to trigger an agent by id.`,
	Example: `  docsgpt-cli agents export <id> -o agents/support.yaml
  docsgpt-cli agents plan -f agents/
  docsgpt-cli agents apply -f agents/ --resolve "source:Handbook=<source-id>"
  docsgpt-cli agents trigger <id> -f payload.json --wait`,
}

var agentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your agents",
	Example: `  docsgpt-cli agents list
  docsgpt-cli agents list --json | jq -r '.[] | select(.status == "published") | .id'`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runAgentsList(ctx, c, agentsListJSON, os.Stdout)
		})
	},
}

var agentsExportCmd = &cobra.Command{
	Use:   "export <id>",
	Short: "Export an agent as YAML (stdout by default)",
	Long: `Export an agent definition as portable YAML. References (sources, tools,
prompt, models) are exported by name and secrets are never included, so the
file is safe to commit.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runAgentsExport(ctx, c, args[0], agentsExportOut, os.Stdout, os.Stderr)
		})
	},
}

var agentsPlanCmd = &cobra.Command{
	Use:   "plan -f <file|dir|->",
	Short: "Show what apply would do, without writing anything",
	Long: `Match every reference of the agent YAML against your account and print the
plan, like 'agents apply --dry-run'. Exits 1 when a reference is unresolved.

` + resolveHelp,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runAgentsApply(ctx, c, applyOptions{
				Files: agentsPlanFiles, Resolve: agentsPlanResolv, DryRun: true, JSON: agentsPlanJSON,
			}, os.Stdin, os.Stdout, os.Stderr)
		})
	},
}

var agentsApplyCmd = &cobra.Command{
	Use:   "apply -f <file|dir|-> [-f ...]",
	Short: "Create or update agents from YAML",
	Long: `Create or update agents from YAML (kind: Agent). -f takes a file, a directory
of *.yaml files, or - for stdin, and can be repeated.

Everything is planned first; if any reference is unresolved, nothing is
applied. An agent is matched by metadata.id, then metadata.slug, and updated
in place; anything else is created as a draft.

` + resolveHelp + `

Exit codes: 0 applied, 1 blocked or failed, 2 usage error.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			return runAgentsApply(ctx, c, applyOptions{
				Files: agentsFiles, Resolve: agentsResolve, DryRun: agentsDryRun, JSON: agentsApplyJSON,
			}, os.Stdin, os.Stdout, os.Stderr)
		})
	},
}

var agentsDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete an agent",
	Long:  "Delete an agent. It asks first; without a terminal it needs --yes.",
	Example: `  docsgpt-cli agents delete <id>
  docsgpt-cli agents delete <id> --yes   # in CI`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			if !agentsDeleteYes {
				if err := confirmDestructive("Delete agent " + args[0] + "?"); err != nil {
					return err
				}
			}
			if err := c.DeleteAgent(ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, display.Success("deleted"), "agent", args[0])
			return nil
		})
	},
}

var agentsTriggerCmd = &cobra.Command{
	Use:   "trigger [<agent-id>] -f <file|->",
	Short: "Run an agent through its incoming webhook with a JSON payload",
	Long: `Post a JSON payload to an agent's incoming webhook; the payload becomes the
agent's input. The run is queued and its task id printed; --wait waits for it
and prints the answer.

Name the agent by its webhook URL (--webhook-url, else ` + config.EnvWebhookURL + `;
no token needed, and the URL is never printed) or by <agent-id> (looked up
with a personal access token, scope agents:keys).

Exit codes: 0 ok, 1 the call or the run failed or timed out, 2 usage error.`,
	Example: `  docsgpt-cli agents trigger --webhook-url "$TRIAGE_WEBHOOK_URL" -f payload.json
  echo '{"event":"deploy"}' | docsgpt-cli agents trigger <agent-id> -f - --wait
  docsgpt-cli agents trigger <agent-id> -f payload.json --wait --json | jq -r .answer`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := triggerOptions{
			WebhookURL: agentsTriggerWebhook,
			File:       agentsTriggerFile,
			Key:        agentsTriggerKey,
			Wait:       agentsTriggerWait,
			Timeout:    agentsTriggerTO,
			JSON:       agentsTriggerJSON,
			UserAgent:  userAgent(),
		}
		if len(args) == 1 {
			opts.AgentID = args[0]
		}
		if !cmd.Flags().Changed("webhook-url") && opts.AgentID == "" {
			opts.WebhookURL = strings.TrimSpace(os.Getenv(config.EnvWebhookURL))
		}
		// Load falls back to the defaults on error; only the agent-id path
		// cannot do without the config.
		cfg, err := config.Load()
		if err != nil && opts.AgentID != "" {
			return usageErrf("load config: %w", err)
		}
		opts.BaseURL = cfg.ResolveURL(globalURL)
		opts.Token, _ = cfg.ResolveToken(globalToken)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return runAgentsTrigger(ctx, opts, os.Stdin, os.Stdout, os.Stderr)
	},
}

func init() {
	agentsListCmd.Flags().BoolVar(&agentsListJSON, "json", false, "Print the server's agent list as JSON")
	agentsExportCmd.Flags().StringVarP(&agentsExportOut, "output", "o", "", "Write the YAML to this file instead of stdout")

	pf := agentsPlanCmd.Flags()
	pf.StringArrayVarP(&agentsPlanFiles, "file", "f", nil, "Agent YAML: a file, a directory, or - for stdin (repeatable)")
	pf.StringArrayVar(&agentsPlanResolv, "resolve", nil, "Settle a reference: <kind>:<selector>=<value> (repeatable; see above)")
	pf.BoolVar(&agentsPlanJSON, "json", false, "Print the plan as JSON")

	af := agentsApplyCmd.Flags()
	af.StringArrayVarP(&agentsFiles, "file", "f", nil, "Agent YAML: a file, a directory, or - for stdin (repeatable)")
	af.StringArrayVar(&agentsResolve, "resolve", nil, "Settle a reference: <kind>:<selector>=<value> (repeatable; see above)")
	af.BoolVar(&agentsDryRun, "dry-run", false, "Stop after the plan; write nothing")
	af.BoolVar(&agentsApplyJSON, "json", false, "Print plan and results as JSON on stdout (human plan goes to stderr)")

	agentsDeleteCmd.Flags().BoolVarP(&agentsDeleteYes, "yes", "y", false, "Do not ask for confirmation")

	tf := agentsTriggerCmd.Flags()
	tf.StringVar(&agentsTriggerWebhook, "webhook-url", "", "The agent's incoming webhook URL (default $"+config.EnvWebhookURL+"); never printed")
	tf.StringVarP(&agentsTriggerFile, "file", "f", "", "JSON payload: a file, or - for stdin (required)")
	tf.StringVar(&agentsTriggerKey, "idempotency-key", "", "Idempotency-Key header: a repeat within ~24h returns the original task")
	tf.BoolVar(&agentsTriggerWait, "wait", false, "Wait for the run to finish and print the answer; exit non-zero if it fails")
	tf.DurationVar(&agentsTriggerTO, "timeout", 10*time.Minute, "How long --wait polls before giving up (e.g. 90s, 15m)")
	tf.BoolVar(&agentsTriggerJSON, "json", false, "Print the result as JSON on stdout")

	agentsCmd.AddCommand(agentsListCmd, agentsExportCmd, agentsPlanCmd, agentsApplyCmd, agentsDeleteCmd, agentsTriggerCmd,
		agentsPromptsCmd, agentsToolsCmd)
	groupCommand(agentsCmd)
}

// withClient builds the account-level client and runs fn under an
// interrupt-aware context.
func withClient(fn func(ctx context.Context, c *manage.Client) error) error {
	client, err := newManageClient()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return fn(ctx, client)
}

func runAgentsList(ctx context.Context, c *manage.Client, asJSON bool, out io.Writer) error {
	agents, raw, err := c.ListAgents(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, raw)
	}
	if len(agents) == 0 {
		fmt.Fprintln(out, "No agents.")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTYPE\tSTATUS\tSLUG\tOWNERSHIP")
	for _, a := range agents {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", textOrDash(a.ID), textOrDash(a.Name), textOrDash(a.AgentType),
			textOrDash(a.Status), textOrDash(a.Slug), textOrDash(a.Ownership))
	}
	return tw.Flush()
}

func runAgentsExport(ctx context.Context, c *manage.Client, id, outPath string, stdout, stderr io.Writer) error {
	doc, err := c.ExportAgent(ctx, id)
	if err != nil {
		return err
	}
	if outPath == "" || outPath == "-" {
		_, err = stdout.Write(doc)
		return err
	}
	if err := os.WriteFile(outPath, doc, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(stderr, display.Success("exported"), "agent", id, "to", outPath)
	return nil
}

// applyOptions are the inputs of `agents plan` / `agents apply`.
type applyOptions struct {
	Files   []string
	Resolve []string
	DryRun  bool
	JSON    bool
}

// applyDocReport is one document's entry in the --json output.
type applyDocReport struct {
	Source   string              `json:"source"`
	Name     string              `json:"name"`
	Plan     json.RawMessage     `json:"plan"`
	Blockers []manage.Blocker    `json:"blockers"`
	Notices  []string            `json:"notices,omitempty"`
	Result   *manage.ApplyResult `json:"result,omitempty"`
	Error    string              `json:"error,omitempty"`
}

// applyReport is the --json document of `agents plan` / `agents apply`.
type applyReport struct {
	DryRun    bool             `json:"dry_run"`
	Blocked   bool             `json:"blocked"`
	Applied   int              `json:"applied"`
	Documents []applyDocReport `json:"documents"`
}

// runAgentsApply loads and validates every document, plans ALL of them, and
// only when no document is blocked applies them in order. With opts.JSON the
// machine document goes to stdout and the human plan to stderr.
func runAgentsApply(ctx context.Context, c *manage.Client, opts applyOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(opts.Files) == 0 {
		return usageErrf("no input: pass -f <file|dir|->")
	}
	resolves, err := manage.ParseResolve(opts.Resolve)
	if err != nil {
		return usageErr(err)
	}
	docs, err := manage.LoadDocuments(opts.Files, stdin)
	if err != nil {
		return usageErr(err)
	}
	if err := resolves.CheckDocumentCount(len(docs)); err != nil {
		return usageErr(err)
	}

	human := stdout
	if opts.JSON {
		human = stderr
	}

	// Phase 1: plan everything. Nothing is written server-side.
	type planned struct {
		doc      manage.Document
		decision *manage.Decision
	}
	report := applyReport{DryRun: opts.DryRun}
	var plans []planned
	for _, doc := range docs {
		plan, err := c.PlanImport(ctx, doc.Text)
		if err != nil {
			return fmt.Errorf("plan %s: %w", doc.Label(), err)
		}
		decision, err := resolves.Decide(plan)
		if err != nil {
			return usageErr(fmt.Errorf("%s: %w", doc.Label(), err))
		}
		printPlan(human, doc, plan, decision)
		plans = append(plans, planned{doc: doc, decision: decision})
		blockers := decision.Blockers
		if blockers == nil {
			blockers = []manage.Blocker{}
		}
		report.Documents = append(report.Documents, applyDocReport{
			Source: doc.Source, Name: doc.Name, Plan: plan.Raw, Blockers: blockers, Notices: decision.Notices,
		})
		if decision.Blocked() {
			report.Blocked = true
		}
	}
	if unused := resolves.Unused(); len(unused) > 0 {
		return usageErrf("--resolve %s does not match any reference in the supplied document(s)", strings.Join(unused, ", "))
	}

	finish := func(runErr error) error {
		if opts.JSON {
			if err := writeJSON(stdout, report); err != nil && runErr == nil {
				return err
			}
		}
		return runErr
	}

	if report.Blocked {
		n := 0
		for _, d := range report.Documents {
			n += len(d.Blockers)
		}
		verb := "nothing was applied"
		if opts.DryRun {
			verb = "an apply would be refused"
		}
		return finish(&exitError{code: exitFailure, err: fmt.Errorf("%d unresolved reference(s): %s (settle them with --resolve, see 'docsgpt-cli agents apply --help')", n, verb)})
	}
	if opts.DryRun {
		fmt.Fprintln(human, display.Muted(fmt.Sprintf("dry run: %d document(s) planned, nothing applied", len(plans))))
		return finish(nil)
	}

	// Phase 2: apply in order; stop at the first failure.
	for i, p := range plans {
		res, err := c.ApplyImport(ctx, p.doc.Text, p.decision.Resolution)
		if err != nil {
			report.Documents[i].Error = err.Error()
			msg := fmt.Errorf("apply %s: %w", p.doc.Label(), err)
			if report.Applied > 0 {
				msg = fmt.Errorf("%w (%d earlier document(s) were already applied)", msg, report.Applied)
			}
			return finish(msg)
		}
		report.Applied++
		report.Documents[i].Result = res
		fmt.Fprintf(human, "%s %s: agent %s %s (status %s, slug %s)\n", display.Success("applied"),
			p.doc.Label(), textOrDash(res.AgentID), textOrDash(res.Action), textOrDash(res.Status), textOrDash(res.Slug))
		for _, w := range res.Warnings {
			warnLine(human, w)
		}
	}
	return finish(nil)
}

// printPlan renders one document's plan: create vs update, every reference
// with its status, the workflow change, and what blocks the apply.
func printPlan(w io.Writer, doc manage.Document, plan *manage.Plan, d *manage.Decision) {
	fmt.Fprintln(w, display.Accent(doc.Label()))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	switch plan.Target.Action {
	case "update":
		fmt.Fprintf(tw, "  agent\tUPDATE\t%s\tmatched by %s, status %s (kept)\n", textOrDash(plan.Target.AgentID),
			textOrDash(plan.Target.MatchedBy), textOrDash(plan.Target.Status))
	default:
		fmt.Fprintf(tw, "  agent\tCREATE\t%s\tnew draft agent\n", doc.Name)
	}

	switch plan.Prompt.Status {
	case "", manage.StatusDefault:
		fmt.Fprintf(tw, "  prompt\tdefault\t\t\n")
	default:
		fmt.Fprintf(tw, "  prompt\t%s\t%q\t\n", plan.Prompt.Status, plan.Prompt.Name)
	}
	for _, s := range plan.Sources {
		fmt.Fprintf(tw, "  source\t%s\t%q\t%s\n", statusText(s.Status), s.Name,
			detail(idNote(s.TargetID), d.Covered["source:"+s.Name]))
	}
	for _, t := range plan.Tools {
		label := t.Type
		if t.Name != "" {
			label = fmt.Sprintf("%s %q", t.Type, t.Name)
		}
		note := idNote(t.TargetID)
		if t.Status == manage.StatusCreate && len(t.RequiresSecrets) > 0 {
			note = "needs secrets: " + strings.Join(t.RequiresSecrets, ", ")
		}
		fmt.Fprintf(tw, "  tool %s\t%s\t%s\t%s\n", display.Safe(t.Key), statusText(t.Status), display.Safe(label), detail(note, d.Covered["tool:"+t.Key]))
	}
	for _, m := range plan.Models {
		note := ""
		if m.Status == manage.StatusCreate && len(m.RequiresSecrets) > 0 {
			note = "needs secrets: " + strings.Join(m.RequiresSecrets, ", ")
		}
		fmt.Fprintf(tw, "  model\t%s\t%s\t%s\n", statusText(m.Status), display.Safe(m.Label()), detail(note, d.Covered["model:"+m.Label()]))
	}
	if wf := plan.Workflow; wf != nil {
		note := fmt.Sprintf("%d nodes, %d edges", wf.Nodes, wf.Edges)
		if wf.Action == "delete" {
			note += " — the graph, its run history and artifacts will be DELETED"
		}
		fmt.Fprintf(tw, "  workflow\t%s\t\t%s\n", strings.ToUpper(wf.Action), note)
	}
	tw.Flush()

	for _, n := range d.Notices {
		warnLine(w, n)
	}
	for _, b := range d.Blockers {
		fmt.Fprintln(w, " ", display.Danger("blocked:"), b.String())
	}
	fmt.Fprintln(w)
}

// statusText upper-cases the statuses that stop an apply so they stand out.
func statusText(status string) string {
	switch status {
	case manage.StatusMissing, manage.StatusUnavailable:
		return strings.ToUpper(status)
	}
	return display.Safe(status)
}

func idNote(id string) string {
	if id == "" {
		return ""
	}
	return "-> " + id
}

func detail(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return display.Safe(strings.Join(out, "; "))
}

// triggerOptions are the inputs of `agents trigger`.
type triggerOptions struct {
	AgentID    string
	WebhookURL string // --webhook-url, else DOCSGPT_WEBHOOK_URL when no agent id is given
	File       string
	Key        string
	Wait       bool
	Timeout    time.Duration
	JSON       bool

	BaseURL   string // --url > DOCSGPT_URL > config
	Token     string // --token > DOCSGPT_TOKEN > config; "" when none
	UserAgent string

	// Poll tunes the --wait backoff; zero values use manage.TriggerPoll.
	Poll manage.WaitOptions
}

// runAgentsTrigger posts the payload to the agent's webhook and, with Wait,
// polls the run. Progress goes to stderr; stdout carries the task id, the
// answer, or the JSON report. The webhook URL never reaches either stream.
func runAgentsTrigger(ctx context.Context, opts triggerOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	switch {
	case opts.AgentID != "" && opts.WebhookURL != "":
		return usageErrf("pass either an agent id or --webhook-url, not both")
	case opts.AgentID == "" && opts.WebhookURL == "":
		return usageErrf("no agent: pass an agent id or --webhook-url (or set %s)", config.EnvWebhookURL)
	case opts.File == "":
		return usageErrf("no payload: pass -f <file>, or -f - to read it from stdin")
	case opts.Wait && opts.Timeout <= 0:
		return usageErrf("--timeout must be positive")
	case len(opts.Key) > manage.IdempotencyKeyMaxLen:
		return usageErrf("--idempotency-key exceeds %d characters", manage.IdempotencyKeyMaxLen)
	}
	payload, err := manage.ReadPayload(opts.File, stdin)
	if err != nil {
		return usageErr(err)
	}

	report, err := manage.Trigger(ctx, manage.TriggerOptions{
		AgentID: opts.AgentID, WebhookURL: opts.WebhookURL, Payload: payload, Key: opts.Key,
		Wait: opts.Wait, Timeout: opts.Timeout, Poll: opts.Poll,
		BaseURL: opts.BaseURL, Token: opts.Token, UserAgent: opts.UserAgent, Log: stderr,
	})
	if report == nil {
		return err
	}
	if err == nil && report.Waited {
		summary := fmt.Sprintf("done in %.0fs", report.Elapsed.Seconds())
		if report.ToolCalls > 0 {
			summary += fmt.Sprintf(", %d tool call(s)", report.ToolCalls)
		}
		fmt.Fprintln(stderr, display.Success("ok"), summary)
	}
	if opts.JSON {
		if jsonErr := writeJSON(stdout, report); jsonErr != nil && err == nil {
			return jsonErr
		}
		return err
	}
	if err == nil {
		switch {
		case report.Deduplicated:
		case report.Waited:
			if answer := strings.TrimRight(report.Answer, "\n"); answer != "" {
				fmt.Fprintln(stdout, answer)
			}
		default:
			fmt.Fprintln(stdout, report.TaskID)
		}
	}
	return err
}
