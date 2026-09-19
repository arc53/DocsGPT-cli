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

	"docsgpt-cli/internal/display"
	"docsgpt-cli/internal/manage"

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
)

const resolveHelp = `Unresolved references are settled with --resolve <kind>:<selector>=<value>
(repeatable), which maps onto the import API's "resolution" object:

  source:<name>=<source-id>            attach this existing source for the named spec source
  source:<name>=skip                   accept that the source stays unattached
  tool:<sel>=reuse:<tool-id>           link one of your existing tools
  tool:<sel>=create                    create a new tool even if a (type, name) match exists
  tool:<sel>=skip                      leave the tool off the agent
  tool:<sel>.secret.<field>=<value>    secret used when the tool is created (e.g. token)
  model:<display-name>=<api-key>       API key used to create a custom model
  model:<id-or-name>=skip              accept that the model is dropped

<sel> is the plan key (tool-0, tool-1, ... = position in spec.tools) or, when
unambiguous, the tool's name or type. Positional keys are rejected when several
documents are processed at once.`

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "List, export, plan, apply and delete agents (personal access token)",
	Long: `Manage agents as code with a personal access token.

  docsgpt-cli agents list
  docsgpt-cli agents export <id> -o agents/support.yaml
  docsgpt-cli agents plan -f agents/
  docsgpt-cli agents apply -f agents/ --resolve "source:Handbook=<source-id>"
  docsgpt-cli agents delete <id> --yes

Scopes: agents:read (list, export) and agents:write (plan, apply, delete).`,
}

var agentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your agents",
	Args:  usageArgs(cobra.NoArgs),
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
	Short: "Show what applying agent YAML would do (nothing is written)",
	Long: `Resolve every reference of the given agent documents against your account
and print the plan. Equivalent to 'agents apply --dry-run': exits 1 when a
reference is missing/unavailable and not covered by --resolve.

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
	Long: `Apply agent documents. -f accepts a file, a directory (its *.yaml/*.yml
files, sorted) or - for stdin, and may be repeated; multi-document files are
applied document by document. Only kind: Agent is accepted.

Every document is planned first and the plan is printed. If any document has a
missing or unavailable reference that --resolve does not cover, NOTHING is
applied and the command exits 1. An agent is matched by metadata.id, then
metadata.slug: a match is updated in place (keeping its status and API key),
anything else is created as a draft.

` + resolveHelp + `

Exit codes: 0 applied (or a clean --dry-run), 1 blocked or failed, 2 usage or
validation error.`,
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
	Args:  usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(func(ctx context.Context, c *manage.Client) error {
			if !agentsDeleteYes {
				if err := confirmDestructive(os.Stdin, os.Stderr, stdinIsTerminal(), "Delete agent "+args[0]+"?"); err != nil {
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

	agentsCmd.AddCommand(agentsListCmd, agentsExportCmd, agentsPlanCmd, agentsApplyCmd, agentsDeleteCmd)
	markManagement(agentsCmd)
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", a.ID, textOrDash(a.Name), textOrDash(a.AgentType),
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
			p.doc.Label(), res.AgentID, res.Action, textOrDash(res.Status), textOrDash(res.Slug))
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
		fmt.Fprintf(tw, "  agent\tUPDATE\t%s\tmatched by %s, status %s (kept)\n", plan.Target.AgentID,
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
		fmt.Fprintf(tw, "  tool %s\t%s\t%s\t%s\n", t.Key, statusText(t.Status), label, detail(note, d.Covered["tool:"+t.Key]))
	}
	for _, m := range plan.Models {
		note := ""
		if m.Status == manage.StatusCreate && len(m.RequiresSecrets) > 0 {
			note = "needs secrets: " + strings.Join(m.RequiresSecrets, ", ")
		}
		fmt.Fprintf(tw, "  model\t%s\t%s\t%s\n", statusText(m.Status), m.Label(), detail(note, d.Covered["model:"+m.Label()]))
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
	return status
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
	return strings.Join(out, "; ")
}
