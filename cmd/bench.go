package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"docsgpt-cli/internal/bench/report"
	"docsgpt-cli/internal/bench/runner"
	"docsgpt-cli/internal/bench/spec"
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
)

var benchCmd = &cobra.Command{
	Use:   "bench [dir]",
	Short: "Run a directory of benchmark cases against DocsGPT agents",
	Long: `Run a suite of benchmark cases against DocsGPT agents and assert on the answers.

A suite is a directory (default ./bench) with an optional bench.yaml (shared
defaults) and one sub-directory per case, each holding a case.yaml (plus any
attachment files). Each case sends a question to an agent through one of three
targets and checks the answer:

  target: v1       POST /v1/chat/completions  (Bearer auth, reports usage)
  target: stream   POST /stream               (api_key in body, SSE)
  target: webhook  POST <webhook_url>         (async, polled to completion)

Assertions live under a case's expect: block (answer text, JSON paths, sources,
tools, limits, golden.json, or an LLM-as-judge rubric).

Examples:
  docsgpt-cli bench                     # run ./bench
  docsgpt-cli bench ./suite -k retrieval
  docsgpt-cli bench --repeat 3 --min-pass 2
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

	addSharedBenchFlags(benchRecordCmd)

	benchCmd.AddCommand(benchRecordCmd)
	benchCmd.AddCommand(benchInitCmd)
}

// addSharedBenchFlags registers the flags common to `bench` and `bench record`.
func addSharedBenchFlags(c *cobra.Command) {
	f := c.Flags()
	f.StringVar(&benchTarget, "target", "", "Override the target for every case (v1, stream, webhook)")
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

	suite, err := spec.Load(dir)
	if err != nil {
		benchFatal(err.Error())
	}

	cases := suite.Filter(benchFilter, splitCSV(benchTags))
	if len(cases) == 0 {
		benchFatal(fmt.Sprintf("no cases match in %s", dir))
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

	opts := runner.Options{
		Suite:                suite,
		Cases:                cases,
		ResolveKey:           resolver,
		BaseURL:              cfg.ResolveURL(""),
		URLOverride:          globalURL,
		AgentOverride:        globalKey,
		TargetOverride:       benchTarget,
		WebhookURLOverride:   benchWebhookURL,
		Concurrency:          concurrency,
		TimeoutOverride:      benchCaseTimeout,
		PollIntervalOverride: benchPollInterval,
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

# Wire protocol: v1 (default), stream, or webhook. Overridable with --target.
target: v1

# Override the API base URL for this suite (defaults to your CLI config).
# base_url: https://gptcloud.arc53.com

# Required only for target: webhook. The URL contains the secret token, so
# prefer ${VAR} or the --webhook-url flag over a literal value here.
# webhook_url: ${DOCSGPT_WEBHOOK_URL}

# Default LLM-as-judge agent for cases that use an expect.judge rubric.
# judge:
#   agent: judge-agent
#   base_url: https://gptcloud.arc53.com

# Run cases in parallel (default 1). Overridable with --concurrency.
# concurrency: 4

# Per-run timeout (default 120s) and webhook/attachment poll cadence (default 2s).
# timeout: 120s
# poll_interval: 2s

# Repeat each case N times and require M passes (flaky-tolerance).
# repeat: 3
# min_pass: 2
`

const caseYAMLTemplate = `description: A minimal example case
tags: [example]

question: What is DocsGPT?

expect:
  answer:
    contains:
      - example
  # sources:
  #   min: 1
  # limits:
  #   max_seconds: 30
  #   max_total_tokens: 2000
  # judge:
  #   rubric: The answer accurately describes DocsGPT as an open-source documentation assistant.
  #   min_score: 0.7
  # golden: true   # compare against golden.json (see: docsgpt-cli bench record)
`
