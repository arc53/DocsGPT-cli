package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"docsgpt-cli/internal/bench/judge"
	"docsgpt-cli/internal/bench/report"
	"docsgpt-cli/internal/bench/runner"
	"docsgpt-cli/internal/bench/spec"
	"docsgpt-cli/internal/bench/target"
	"docsgpt-cli/internal/config"
	"docsgpt-cli/internal/display"

	"github.com/spf13/cobra"
)

// Shared bench flags. The same variables back `bench` and `bench record`; only
// the invoked command parses its own flag set, so reuse is safe.
var (
	benchTarget       string
	benchFilter       string
	benchTags         string
	benchConcurrency  int
	benchRepeat       int
	benchMinPass      int
	benchCaseTimeout  time.Duration
	benchPollInterval time.Duration
	benchFailFast     bool
	benchJSON         bool
	benchJUnit        string
	benchNoSave       bool
	benchBaseline     string
	benchUpdate       bool
	benchVerbose      bool
	benchVS           []string
	benchJudge        string
	benchWebhookURL   string
	benchModel        string
	benchMatrix       string
	benchRunTag       string
)

var benchCmd = &cobra.Command{
	Use:   "bench [dir]",
	Short: "Run a directory of benchmark cases against DocsGPT agents",
	Long: `Run a suite of benchmark cases against DocsGPT agents and assert on the answers.

A suite is a directory (default ./bench) with an optional bench.yaml (shared
defaults) and one sub-directory per case, each holding a case.yaml (plus any
attachment files). Each case sends a question (or a sequence of turns) to an
agent through one of four targets and checks the answer:

  target: v1       POST /v1/chat/completions  (Bearer auth, reports usage; stream: true for SSE)
  target: stream   POST /stream               (api_key in body, SSE, records TTFT + frames)
  target: answer   POST /api/answer           (api_key in body, single JSON reply)
  target: webhook  POST <webhook_url>         (async, polled to completion)

Assertions live under a case's expect: block (answer text, JSON paths, sources,
tools, limits incl. time-to-first-token, stream integrity, an expected server
error for negative cases, golden.json, or an LLM-as-judge rubric).

Examples:
  docsgpt-cli bench                     # run ./bench
  docsgpt-cli bench ./suite -k retrieval
  docsgpt-cli bench --repeat 3 --min-pass 2
  docsgpt-cli bench --model gpt-5.6-terra          # pin one model for every case
  docsgpt-cli bench --matrix m1,m2,m3 --tags hard   # run once per model, compare
  docsgpt-cli bench --vs staging-agent  # A/B two agents
  docsgpt-cli bench init                # scaffold a starter suite
  docsgpt-cli bench record              # refresh golden answers`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runBenchSuite(args, false)
		return nil
	},
}

var benchRecordCmd = &cobra.Command{
	Use:   "record [dir]",
	Short: "Record golden answers for cases (overwrites golden.json)",
	Long: `Run each case once and write its answer to golden.json in the case directory.
Existing goldens are overwritten. Assertions still run and are reported, but the
recorded answer is saved regardless of whether they pass.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runBenchSuite(args, true)
		return nil
	},
}

var benchInitCmd = &cobra.Command{
	Use:   "init [dir]",
	Short: "Scaffold a starter benchmark suite",
	Long:  "Create a bench.yaml and an example case in dir (default ./bench). Existing files are left untouched.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBenchInit(args)
	},
}

func init() {
	addSharedBenchFlags(benchCmd)
	f := benchCmd.Flags()
	f.IntVar(&benchRepeat, "repeat", 0, "Runs per case (overrides suite/case repeat)")
	f.IntVar(&benchMinPass, "min-pass", 0, "Passing runs required per case (overrides min_pass)")
	f.BoolVar(&benchNoSave, "no-save", false, "Do not save this run to history")
	f.StringVar(&benchBaseline, "baseline", "", "Diff against a baseline: 'last' or a run-history file path")
	f.BoolVar(&benchUpdate, "update", false, "Refresh golden.json files during this run (record semantics)")
	f.StringArrayVar(&benchVS, "vs", nil, "Also run against this agent and print an A/B comparison (repeatable)")
	f.StringVar(&benchMatrix, "matrix", "", "Run the suite once per model (comma-separated model ids) and print a comparison table")

	addSharedBenchFlags(benchRecordCmd)

	benchCmd.AddCommand(benchRecordCmd)
	benchCmd.AddCommand(benchInitCmd)
}

// addSharedBenchFlags registers the flags common to `bench` and `bench record`.
func addSharedBenchFlags(c *cobra.Command) {
	f := c.Flags()
	f.StringVar(&benchTarget, "target", "", "Override the target for every case (v1, stream, answer, webhook)")
	f.StringVar(&benchModel, "model", "", "Model id sent with every request (overrides suite/case model)")
	f.StringVar(&benchRunTag, "run-tag", "", "Tag sent as X-DocsGPT-Bench-Tag: bench:<tag> so server telemetry can attribute bench traffic")
	f.StringVar(&benchWebhookURL, "webhook-url", "", "Override webhook_url for every case (keeps the token out of YAML)")
	f.StringVarP(&benchFilter, "filter", "k", "", "Only run cases whose name or description contains this text")
	f.StringVar(&benchTags, "tags", "", "Only run cases carrying at least one of these tags (comma-separated)")
	f.IntVar(&benchConcurrency, "concurrency", 0, "Number of cases to run in parallel (default: suite config or 1)")
	f.DurationVar(&benchCaseTimeout, "case-timeout", 0, "Per-run timeout override (e.g. 90s, 2m)")
	f.DurationVar(&benchPollInterval, "poll-interval", 0, "Webhook/attachment polling interval override")
	f.BoolVar(&benchFailFast, "fail-fast", false, "Stop scheduling new cases after the first failure/error")
	f.BoolVar(&benchJSON, "json", false, "Emit only the JSON result document on stdout (progress to stderr)")
	f.StringVar(&benchJUnit, "junit", "", "Also write a JUnit XML report to this file")
	f.BoolVar(&benchVerbose, "verbose", false, "Show each run's answer and judge reasoning")
	f.StringVar(&benchJudge, "judge", "", "Judge agent (key name or literal key) for expect.judge sections")
}

// runBenchSuite loads, runs, reports, and (optionally) persists a suite, then
// exits the process with the appropriate code. record=true forces golden
// recording semantics.
func runBenchSuite(args []string, record bool) {
	dir := "./bench"
	if len(args) > 0 {
		dir = args[0]
	}
	target.UserAgent = "docsgpt-cli/" + Version + " bench"
	judge.UserAgent = target.UserAgent

	suite, err := spec.Load(dir)
	if err != nil {
		benchFatal(err.Error())
	}

	cases := suite.Filter(benchFilter, splitCSV(benchTags))
	if len(cases) == 0 {
		benchFatal(fmt.Sprintf("no cases match in %s", dir))
	}

	matrixModels := splitCSV(benchMatrix)
	if len(matrixModels) > 0 && record {
		benchFatal("--matrix cannot be combined with bench record")
	}
	if len(matrixModels) > 0 && benchModel != "" {
		benchFatal("--matrix and --model are mutually exclusive")
	}
	if len(matrixModels) > 0 && len(benchVS) > 0 {
		benchFatal("--matrix and --vs are mutually exclusive")
	}

	cfg, err := config.Load()
	if err != nil {
		benchFatal("load config: " + err.Error())
	}

	resolver := func(nameOrKey string) (string, string, error) {
		if nameOrKey == "" {
			return "", "", fmt.Errorf("empty agent reference")
		}
		if v, ok := cfg.Keys[nameOrKey]; ok {
			return v, nameOrKey, nil
		}
		return nameOrKey, literalKeyLabel(nameOrKey), nil
	}

	// Judge: --judge flag wins over the suite's bench.yaml judge.
	var judgeAgent *spec.JudgeAgent
	switch {
	case benchJudge != "":
		judgeAgent = &spec.JudgeAgent{Agent: benchJudge}
	case suite.Config.Judge != nil:
		judgeAgent = suite.Config.Judge
	}

	concurrency := benchConcurrency
	if concurrency <= 0 {
		concurrency = suite.Config.Concurrency
	}
	if concurrency <= 0 {
		concurrency = 1
	}

	var goldenPaths []string
	onEvent := func(e runner.Event) {
		switch e.Type {
		case runner.EventGolden:
			goldenPaths = append(goldenPaths, e.Msg)
		case runner.EventCaseDone:
			if !benchJSON {
				fmt.Fprintf(os.Stderr, "  %s %s\n", liveTag(e.Msg), e.Case)
			}
		}
	}

	runTag := benchRunTag
	if runTag == "" {
		runTag = suite.Config.RunTag
	}

	opts := runner.Options{
		Suite:                suite,
		Cases:                cases,
		ResolveKey:           resolver,
		BaseURL:              cfg.ResolveURL(""),
		URLOverride:          globalURL,
		AgentOverride:        globalKey,
		ModelOverride:        benchModel,
		TargetOverride:       benchTarget,
		WebhookURLOverride:   benchWebhookURL,
		Concurrency:          concurrency,
		TimeoutOverride:      benchCaseTimeout,
		PollIntervalOverride: benchPollInterval,
		RunTag:               runTag,
		FailFast:             benchFailFast,
		UpdateGolden:         record || benchUpdate,
		Judge:                judgeAgent,
		OnEvent:              onEvent,
	}
	if !record {
		opts.RepeatOverride = benchRepeat
		opts.MinPassOverride = benchMinPass
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if len(matrixModels) > 0 {
		if benchBaseline != "" && !benchJSON {
			fmt.Fprintln(os.Stderr, display.Warn("warning:"), "--baseline is ignored in matrix mode (matrix runs are not saved to history)")
		}
		runBenchMatrix(ctx, opts, matrixModels)
		return
	}

	result, err := runner.Run(ctx, opts)
	if err != nil {
		benchFatal(err.Error())
	}

	// A/B runs against extra agents (normal mode only).
	var vsResults []*runner.SuiteResult
	if !record {
		for _, key := range benchVS {
			vopts := opts
			vopts.AgentOverride = key
			vopts.UpdateGolden = false // vs runs must never overwrite goldens
			vr, err := runner.Run(ctx, vopts)
			if err != nil {
				benchFatal(err.Error())
			}
			vsResults = append(vsResults, vr)
		}
	}

	// Primary output.
	if benchJSON {
		if err := report.JSON(os.Stdout, result); err != nil {
			fmt.Fprintln(os.Stderr, display.Danger("Error:"), "write json:", err)
		}
	} else {
		report.Pretty(os.Stdout, result, benchVerbose)
		for _, vr := range vsResults {
			fmt.Fprintln(os.Stdout)
			report.Pretty(os.Stdout, vr, benchVerbose)
		}
		if len(vsResults) > 0 {
			fmt.Fprintln(os.Stdout)
			report.Compare(os.Stdout, append([]*runner.SuiteResult{result}, vsResults...))
		}
	}

	if record || benchUpdate {
		printGoldens(goldenPaths)
	}

	if benchJUnit != "" {
		if err := writeJUnitFile(benchJUnit, result); err != nil {
			fmt.Fprintln(os.Stderr, display.Danger("Error:"), "write junit:", err)
		}
	}

	// Baseline diff + history save (normal, non-vs, saving runs only). An
	// interrupted run must not become the "last" baseline.
	if !record && !benchNoSave && len(benchVS) == 0 && ctx.Err() == nil {
		var base *runner.SuiteResult
		if benchBaseline != "" {
			base, err = report.LoadBaseline(suite.Name, benchBaseline)
			if err != nil {
				fmt.Fprintln(os.Stderr, display.Warn("warning:"), err)
			}
		}
		if base != nil && !benchJSON {
			fmt.Fprintln(os.Stdout)
			report.Diff(os.Stdout, base, result)
		}
		if path, err := report.SaveRun(result); err != nil {
			fmt.Fprintln(os.Stderr, display.Warn("warning:"), "save run:", err)
		} else {
			fmt.Fprintln(os.Stderr, display.Muted("saved run to "+path))
		}
	}

	if ctx.Err() != nil {
		os.Exit(1) // interrupted
	}
	if record || benchUpdate {
		// Golden-recording runs: assertions are informational; the exit code
		// reflects whether the answers could be recorded at all.
		if result.Totals.Error > 0 {
			os.Exit(1)
		}
		return
	}
	if result.Totals.Fail > 0 || result.Totals.Error > 0 {
		os.Exit(1)
	}
}

// runBenchMatrix runs the filtered suite once per model and prints (or emits
// as JSON) the per-model comparison. Matrix runs are never saved to history
// or diffed against a baseline. Exit code: 1 when any model has failures or
// errors, 0 otherwise.
func runBenchMatrix(ctx context.Context, opts runner.Options, models []string) {
	var runs []*runner.SuiteResult
	for i, model := range models {
		if ctx.Err() != nil {
			break
		}
		if !benchJSON {
			fmt.Fprintf(os.Stderr, "%s %s (%d/%d)\n", display.Accent("model:"), model, i+1, len(models))
		}
		mopts := opts
		mopts.ModelOverride = model
		mopts.UpdateGolden = false
		r, err := runner.Run(ctx, mopts)
		if err != nil {
			benchFatal(err.Error())
		}
		runs = append(runs, r)
	}
	matrix := report.NewMatrix(models[:len(runs)], runs)

	if benchJSON {
		if err := report.MatrixJSON(os.Stdout, matrix); err != nil {
			fmt.Fprintln(os.Stderr, display.Danger("Error:"), "write json:", err)
		}
	} else {
		for _, r := range runs {
			report.Pretty(os.Stdout, r, benchVerbose)
			fmt.Fprintln(os.Stdout)
		}
		if len(runs) > 1 {
			report.Compare(os.Stdout, runs)
			fmt.Fprintln(os.Stdout)
		}
		report.Matrix(os.Stdout, matrix)
	}

	if benchJUnit != "" && len(runs) > 0 {
		// One JUnit file cannot hold N suites cleanly; write the first model's
		// run and warn, so CI still gets a signal.
		if err := writeJUnitFile(benchJUnit, runs[0]); err != nil {
			fmt.Fprintln(os.Stderr, display.Danger("Error:"), "write junit:", err)
		} else if len(runs) > 1 {
			fmt.Fprintln(os.Stderr, display.Warn("warning:"), "--junit in matrix mode only records the first model; use --json for the full matrix")
		}
	}

	if ctx.Err() != nil {
		os.Exit(1)
	}
	for _, r := range runs {
		if r.Totals.Fail > 0 || r.Totals.Error > 0 {
			os.Exit(1)
		}
	}
}

// runBenchInit scaffolds a starter suite, never overwriting existing files.
func runBenchInit(args []string) error {
	dir := "./bench"
	if len(args) > 0 {
		dir = args[0]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	caseDir := filepath.Join(dir, "example-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		return err
	}

	var created, skipped []string
	writeIfAbsent := func(path, content string) error {
		if _, err := os.Stat(path); err == nil {
			skipped = append(skipped, path)
			return nil
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
		created = append(created, path)
		return nil
	}

	if err := writeIfAbsent(filepath.Join(dir, spec.SuiteFileName), benchYAMLTemplate); err != nil {
		return err
	}
	if err := writeIfAbsent(filepath.Join(caseDir, spec.CaseFileName), caseYAMLTemplate); err != nil {
		return err
	}

	for _, p := range created {
		fmt.Println(display.Success("created"), p)
	}
	for _, p := range skipped {
		fmt.Println(display.Muted("exists  "), p)
	}
	if len(created) == 0 {
		fmt.Println(display.Muted("nothing to do — all files already exist"))
	}
	return nil
}

func printGoldens(paths []string) {
	w := os.Stdout
	if benchJSON {
		w = os.Stderr
	}
	if len(paths) == 0 {
		fmt.Fprintln(w, display.Muted("no golden files written (every case errored or was skipped)"))
		return
	}
	fmt.Fprintln(w, display.Success(fmt.Sprintf("wrote %d golden file(s):", len(paths))))
	for _, p := range paths {
		fmt.Fprintln(w, "  "+p)
	}
}

func writeJUnitFile(path string, r *runner.SuiteResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return report.JUnit(f, r)
}

// benchFatal reports a setup error to stderr and exits with code 2.
func benchFatal(msg string) {
	fmt.Fprintln(os.Stderr, display.Danger("Error:"), msg)
	os.Exit(2)
}

// liveTag renders a compact colored status for a live progress line.
func liveTag(status string) string {
	switch runner.Status(status) {
	case runner.StatusPass:
		return display.Success("PASS")
	case runner.StatusFail:
		return display.Danger("FAIL")
	case runner.StatusSkip:
		return display.Warn("SKIP")
	case runner.StatusError:
		return display.Danger("ERR ")
	}
	return status
}

// literalKeyLabel builds the display label for an agent reference that is a
// literal API key rather than a configured key name.
func literalKeyLabel(k string) string {
	if len(k) <= 4 {
		return "key:…" + k
	}
	return "key:…" + k[len(k)-4:]
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

const benchYAMLTemplate = `# Suite defaults applied to every case (each case may override any of these).

# Agent that answers the cases: a key name from ~/.docsgpt/config.json, or a
# literal API key. Overridable with --key.
#
# Any value in this file (and in case.yaml files) may reference environment
# variables as ${VAR} — resolved from your shell, the suite's .env, or ./.env
# (in that order; $$ escapes a literal dollar). Commit the YAML, keep the
# secrets in the environment:
# agent: ${DOCSGPT_BENCH_KEY}
# agent: my-agent

# Wire protocol: v1 (default), stream, answer, or webhook. Overridable with --target.
target: v1

# Model id sent with every request (stream/answer: model_id, v1: model). Leave
# unset for the agent's default. Overridable with --model, or run the whole
# suite once per model with --matrix m1,m2,m3.
# model: gpt-5.6-terra

# v1 only: consume the SSE stream (records time-to-first-token) instead of a
# single JSON response.
# stream: true

# How attachments reach the agent: upload (default, /api/store_attachment) or
# inline (v1 only: base64 file content parts in the message).
# attachments_mode: upload

# Override the API base URL for this suite (defaults to your CLI config).
# base_url: https://gptcloud.arc53.com

# Required only for target: webhook. The URL contains the secret token, so
# prefer ${VAR} or the --webhook-url flag over a literal value here.
# webhook_url: ${DOCSGPT_WEBHOOK_URL}

# Default LLM-as-judge agent for cases that use an expect.judge rubric.
# judge:
#   agent: judge-agent
#   base_url: https://gptcloud.arc53.com
#   model: gpt-5.6-sol     # forwarded in the judge chat request
#   temperature: 0

# Tag every request (X-DocsGPT-Bench-Tag: bench:<tag>) so server telemetry can
# attribute — and exclude — bench traffic. Overridable with --run-tag.
# run_tag: nightly

# Prices (USD per 1M tokens) for the cost column, keyed by model id. Used when
# GET /api/models does not expose pricing. Cost needs token usage (v1 target).
# pricing:
#   gpt-5.6-terra: {input_per_million: 1.25, output_per_million: 10}

# Run cases in parallel (default 1). Overridable with --concurrency.
# concurrency: 4

# Per-run timeout (per turn for multi-turn cases; default 120s) and
# webhook/attachment poll cadence (default 2s).
# timeout: 120s
# poll_interval: 2s

# Repeat each case N times and require M passes (flaky-tolerance).
# repeat: 3
# min_pass: 2
`

const caseYAMLTemplate = `description: A minimal example case
tags: [example]

question: What is DocsGPT?

# Multi-turn alternative to question: each turn is sent in order, carrying the
# conversation forward; the case-level expect applies to the LAST answer.
# turns:
#   - question: My project is called Zephyr. Remember that.
#     expect: {answer: {contains: Zephyr}}   # optional per-turn assertions
#   - question: What did I just tell you my project is called?

expect:
  answer:
    contains:
      - example
  # sources:
  #   min: 1
  # limits:
  #   max_seconds: 30
  #   max_first_token_seconds: 5   # time to first output (stream target, or v1 with stream: true)
  #   max_total_tokens: 2000
  # stream:                        # SSE integrity (stream target only)
  #   end_frame: true
  #   error_frame: false
  #   thought: ignore              # present | absent | ignore
  #   frames_contain: [source]
  # judge:
  #   rubric: The answer accurately describes DocsGPT as an open-source documentation assistant.
  #   min_score: 0.7
  # golden: true   # compare against golden.json (see: docsgpt-cli bench record)

# Negative case: a server error is the expected outcome (a successful answer
# then FAILS the case). Cannot be combined with answer/json/judge/... sections.
# expect:
#   error:
#     status: 401           # HTTP status (optional)
#     contains: Invalid     # substring of the error message/body
`
