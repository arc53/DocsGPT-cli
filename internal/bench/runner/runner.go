// Package runner executes a filtered set of benchmark cases against DocsGPT
// agents and collects a structured, JSON-serializable result tree.
//
// Each case resolves its own effective settings (target, agent, model, base
// URL, timeout, repeat/min_pass) from the suite defaults plus runtime
// overrides, sends its question(s) through the chosen wire protocol
// (internal/bench/target), evaluates the answer with internal/bench/assert
// (and, optionally, the LLM-as-judge in internal/bench/judge), and reports a
// per-run/per-case verdict.
package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"docsgpt-cli/internal/bench/assert"
	"docsgpt-cli/internal/bench/judge"
	"docsgpt-cli/internal/bench/pricing"
	"docsgpt-cli/internal/bench/spec"
	"docsgpt-cli/internal/bench/target"
)

// SchemaVersion is stamped into every SuiteResult so persisted run history and
// external consumers can detect format changes.
//
//	1: initial format
//	2: model, first_output_ms, cost_usd, judge {score,reasoning}, turns,
//	   error_response, frames; answer target; matrix documents
const SchemaVersion = 2

// Seams overridable in tests: the target lookup, the attachment uploader, the
// judge call, and the pricing fetch. Production code uses the real
// implementations.
var (
	lookup            = target.ForName
	uploadAttachments = target.UploadAttachments
	judgeRun          = judge.Run
	fetchPricing      = func(ctx context.Context, t *pricing.Table, baseURL string) error { return t.Fetch(ctx, baseURL) }
)

// KeyResolver maps an agent reference (a key name from the config, or a literal
// API key) to its key value and a human-readable display name.
type KeyResolver func(nameOrKey string) (keyValue, displayName string, err error)

// Options configures a single Run.
type Options struct {
	Suite      *spec.Suite
	Cases      []*spec.Case // pre-filtered; empty is an error
	ResolveKey KeyResolver

	BaseURL       string // config base URL; lowest URL precedence
	URLOverride   string // --url; wins over case/suite/config
	AgentOverride string // --key; wins over case/suite agent
	ModelOverride string // --model / --matrix entry; wins over case/suite model

	TargetOverride       string // --target
	WebhookURLOverride   string // --webhook-url; wins over case/suite webhook_url
	Concurrency          int    // resolved by the caller (flag > suite config > 1)
	RepeatOverride       int    // 0 = defer to spec
	MinPassOverride      int    // 0 = defer to spec
	TimeoutOverride      time.Duration
	PollIntervalOverride time.Duration // --poll-interval; 0 = defer to spec
	RunTag               string        // --run-tag / bench.yaml run_tag

	FailFast     bool
	UpdateGolden bool // `bench record`/--update: overwrite golden.json from run 1
	NoPricing    bool // skip the /api/models pricing fetch (tests, offline)

	Judge   *spec.JudgeAgent // resolved default judge agent (may be nil)
	OnEvent func(Event)      // nil-safe live progress callback
}

// Event types emitted through Options.OnEvent.
const (
	EventCaseStart = "case_start"
	EventRunDone   = "run_done"
	EventCaseDone  = "case_done"
	EventGolden    = "golden" // record mode: a golden.json was written (Msg = path)
)

// Event is one live-progress notification. OnEvent is invoked serially even
// when cases run concurrently, so callbacks need not be reentrant.
type Event struct {
	Type string `json:"type"`
	Case string `json:"case"`
	Msg  string `json:"msg,omitempty"`
	Run  int    `json:"run,omitempty"`
}

// Status is a run or case verdict.
type Status string

const (
	StatusPass  Status = "pass"
	StatusFail  Status = "fail"
	StatusSkip  Status = "skip"
	StatusError Status = "error"
)

// SuiteResult is the top-level, JSON-serializable outcome of a Run.
type SuiteResult struct {
	SchemaVersion int           `json:"schema_version"`
	Suite         string        `json:"suite"`
	Dir           string        `json:"dir"`
	AgentLabel    string        `json:"agent_label"`
	Target        string        `json:"target"`
	Model         string        `json:"model,omitempty"` // effective model when every case shares one; "mixed" otherwise
	RunTag        string        `json:"run_tag,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	DurationMS    int64         `json:"duration_ms"`
	Cases         []*CaseResult `json:"cases"`
	Totals        Totals        `json:"totals"`
}

// Totals counts case statuses.
type Totals struct {
	Pass  int `json:"pass"`
	Fail  int `json:"fail"`
	Skip  int `json:"skip"`
	Error int `json:"error"`
}

// CaseResult aggregates one case across its (possibly repeated) runs.
type CaseResult struct {
	Name         string       `json:"name"`
	Description  string       `json:"description,omitempty"`
	Tags         []string     `json:"tags,omitempty"`
	Target       string       `json:"target"`
	Model        string       `json:"model,omitempty"` // effective model ("" = agent default)
	Status       Status       `json:"status"`
	SkipReason   string       `json:"skip_reason,omitempty"`
	Runs         []*RunResult `json:"runs,omitempty"`
	PassedRuns   int          `json:"passed_runs"`
	RequiredPass int          `json:"required_pass"`
	Repeat       int          `json:"repeat"`
	DurationMS   int64        `json:"duration_ms"`
}

// RunResult is one execution of a case: the answer, its assertions, and stats.
type RunResult struct {
	Index         int             `json:"index"`
	Status        Status          `json:"status"`
	Error         string          `json:"error,omitempty"`
	Assertions    []assert.Result `json:"assertions,omitempty"`
	Answer        string          `json:"answer,omitempty"`
	Usage         *target.Usage   `json:"usage,omitempty"`
	LatencyMS     int64           `json:"latency_ms"`
	FirstOutputMS int64           `json:"first_output_ms,omitempty"` // time to first token; 0 = not observed
	CostUSD       *float64        `json:"cost_usd,omitempty"`        // usage × pricing; nil when unknown
	SourceCount   int             `json:"source_count"`
	ToolCalls     []string        `json:"tool_calls,omitempty"`
	Frames        []string        `json:"frames,omitempty"`         // stream target: SSE frame types seen
	Judge         *JudgeResult    `json:"judge,omitempty"`          // recorded verdict (score + reasoning verbatim)
	ErrorResponse *ErrorResponse  `json:"error_response,omitempty"` // negative cases: the server error asserted on
	Turns         []*TurnResult   `json:"turns,omitempty"`          // multi-turn cases: every turn (Answer = last)
}

// JudgeResult is the recorded LLM-as-judge verdict for one run.
type JudgeResult struct {
	Score     float64 `json:"score"`
	MinScore  float64 `json:"min_score"`
	Reasoning string  `json:"reasoning,omitempty"`
	Model     string  `json:"model,omitempty"`
}

// ErrorResponse is the server error a negative case asserted against.
type ErrorResponse struct {
	Status  int    `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

// TurnResult is one turn of a multi-turn run.
type TurnResult struct {
	Index          int             `json:"index"`
	Question       string          `json:"question"`
	Answer         string          `json:"answer,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	LatencyMS      int64           `json:"latency_ms"`
	FirstOutputMS  int64           `json:"first_output_ms,omitempty"`
	Usage          *target.Usage   `json:"usage,omitempty"`
	Assertions     []assert.Result `json:"assertions,omitempty"` // per-turn expect only
}

// Run executes opts.Cases with a worker pool of opts.Concurrency and returns a
// fully populated SuiteResult. Per-case problems (missing agent, upload
// failures, target errors) are captured in the result tree, not returned as an
// error; Run only errors on invalid Options.
func Run(ctx context.Context, opts Options) (*SuiteResult, error) {
	if opts.Suite == nil {
		return nil, fmt.Errorf("runner: suite is nil")
	}
	if len(opts.Cases) == 0 {
		return nil, fmt.Errorf("runner: no cases to run")
	}
	if opts.ResolveKey == nil {
		return nil, fmt.Errorf("runner: ResolveKey is required")
	}

	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(opts.Cases) {
		concurrency = len(opts.Cases)
	}

	rc := &runContext{
		opts:    opts,
		labels:  make(map[string]struct{}),
		prices:  pricing.New(opts.Suite.Config.Pricing),
		fetched: make(map[string]bool),
	}
	start := time.Now()
	results := make([]*CaseResult, len(opts.Cases))
	var stopped atomic.Bool

	idxCh := make(chan int, len(opts.Cases))
	for i := range opts.Cases {
		idxCh <- i
	}
	close(idxCh)

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idxCh {
				// Cancellation (e.g. Ctrl-C) or fail-fast: drain the queue
				// without starting new cases. Cases already in flight finish.
				if ctx.Err() != nil || (opts.FailFast && stopped.Load()) {
					continue
				}
				cr := rc.runCase(ctx, opts.Cases[i])
				results[i] = cr
				if opts.FailFast && (cr.Status == StatusFail || cr.Status == StatusError) {
					stopped.Store(true)
				}
			}
		}()
	}
	wg.Wait()

	sr := &SuiteResult{
		SchemaVersion: SchemaVersion,
		Suite:         opts.Suite.Name,
		Dir:           opts.Suite.Dir,
		StartedAt:     start,
		RunTag:        opts.RunTag,
	}
	var targets, models []string
	for _, cr := range results {
		if cr == nil { // never scheduled (fail-fast) — omit
			continue
		}
		sr.Cases = append(sr.Cases, cr)
		targets = append(targets, cr.Target)
		models = append(models, cr.Model)
		switch cr.Status {
		case StatusPass:
			sr.Totals.Pass++
		case StatusFail:
			sr.Totals.Fail++
		case StatusSkip:
			sr.Totals.Skip++
		case StatusError:
			sr.Totals.Error++
		}
	}
	sr.DurationMS = time.Since(start).Milliseconds()
	sr.AgentLabel = rc.agentLabel()
	sr.Target = distinct(targets)
	sr.Model = distinct(models)
	if opts.ModelOverride != "" {
		sr.Model = opts.ModelOverride
	}
	return sr, nil
}

// runContext carries the shared, concurrency-safe run state.
type runContext struct {
	opts Options

	mu     sync.Mutex
	labels map[string]struct{}

	emu sync.Mutex // serializes OnEvent callbacks

	pmu     sync.Mutex // guards prices/fetched
	prices  *pricing.Table
	fetched map[string]bool // base URLs whose /api/models was already fetched
}

func (rc *runContext) emit(e Event) {
	if rc.opts.OnEvent == nil {
		return
	}
	rc.emu.Lock()
	defer rc.emu.Unlock()
	rc.opts.OnEvent(e)
}

func (rc *runContext) addLabel(name string) {
	if name == "" {
		return
	}
	rc.mu.Lock()
	rc.labels[name] = struct{}{}
	rc.mu.Unlock()
}

func (rc *runContext) agentLabel() string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	switch len(rc.labels) {
	case 0:
		return ""
	case 1:
		for k := range rc.labels {
			return k
		}
	}
	return "mixed"
}

// pricingFor returns the pricing table for baseURL, fetching /api/models the
// first time a base URL is seen (once per run, best effort).
func (rc *runContext) pricingFor(ctx context.Context, baseURL string) *pricing.Table {
	rc.pmu.Lock()
	defer rc.pmu.Unlock()
	if rc.opts.NoPricing || rc.fetched[baseURL] {
		return rc.prices
	}
	rc.fetched[baseURL] = true
	fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_ = fetchPricing(fctx, rc.prices, baseURL) // silent: no pricing → no cost column
	return rc.prices
}

// runCase executes one case end to end and always returns a non-nil result.
func (rc *runContext) runCase(ctx context.Context, c *spec.Case) *CaseResult {
	opts := rc.opts
	eff := opts.effective(c)
	cr := &CaseResult{
		Name:        c.Name,
		Description: c.Description,
		Tags:        []string(c.Tags),
		Target:      eff.Target,
		Model:       eff.Model,
	}
	rc.emit(Event{Type: EventCaseStart, Case: c.Name})
	started := time.Now()
	defer func() {
		cr.DurationMS = time.Since(started).Milliseconds()
		rc.emit(Event{Type: EventCaseDone, Case: c.Name, Msg: string(cr.Status)})
	}()

	if c.Skip != "" {
		cr.Status = StatusSkip
		cr.SkipReason = c.Skip
		return cr
	}

	repeat := eff.Repeat
	required := eff.MinPass
	if opts.UpdateGolden {
		// record mode is a single run regardless of repeat/min_pass.
		repeat, required = 1, 1
	}
	cr.Repeat = repeat
	cr.RequiredPass = required

	resolvedURL := firstNonEmpty(opts.URLOverride, eff.BaseURL, opts.BaseURL)
	agentName := firstNonEmpty(opts.AgentOverride, eff.Agent)
	if agentName == "" {
		setError(cr, "no agent configured (set agent in bench.yaml/case.yaml or pass --key)")
		return cr
	}
	keyValue, displayName, err := opts.ResolveKey(agentName)
	if err != nil {
		setError(cr, "resolve agent: "+err.Error())
		return cr
	}
	rc.addLabel(displayName)

	if resolvedURL == "" {
		setError(cr, "no base URL configured (set base_url or pass --url)")
		return cr
	}

	tg, err := lookup(eff.Target)
	if err != nil {
		setError(cr, err.Error())
		return cr
	}

	// Re-check target constraints against the EFFECTIVE target: a --target
	// override bypasses the loader's validation of the declared target.
	switch eff.Target {
	case spec.TargetWebhook:
		if len(c.Attachments) > 0 {
			setError(cr, "attachments are not supported by the webhook target")
			return cr
		}
		if c.MultiTurn() {
			setError(cr, "turns are not supported by the webhook target")
			return cr
		}
		if eff.WebhookURL == "" {
			setError(cr, "webhook target requires webhook_url (bench.yaml, case.yaml, or --webhook-url)")
			return cr
		}
	case spec.TargetAnswer:
		if len(c.Attachments) > 0 {
			setError(cr, "attachments are not supported by the answer target")
			return cr
		}
	}
	inline := eff.AttachmentsMode == spec.AttachmentsInline && len(c.Attachments) > 0
	if inline && eff.Target != spec.TargetV1 {
		setError(cr, "attachments_mode inline requires the v1 target")
		return cr
	}

	golden, goldenErr := c.LoadGolden()
	if goldenErr != nil && c.Expect.Golden {
		setError(cr, "golden.json unreadable: "+goldenErr.Error()+" (fix it or re-record with `bench record`)")
		return cr
	}

	req := target.Request{
		Model:        eff.Model,
		Stream:       eff.Stream,
		BaseURL:      resolvedURL,
		APIKey:       keyValue,
		WebhookURL:   eff.WebhookURL,
		Timeout:      eff.Timeout,
		PollInterval: eff.PollInterval,
		RunTag:       opts.RunTag,
	}
	if len(c.Attachments) > 0 {
		paths := make([]string, len(c.Attachments))
		for i, a := range c.Attachments {
			paths[i] = filepath.Join(c.Dir, filepath.Clean(a))
		}
		if inline {
			for i, p := range paths {
				req.InlineFiles = append(req.InlineFiles, target.InlineFile{Name: filepath.Base(c.Attachments[i]), Path: p})
			}
		} else {
			aCtx, cancel := context.WithTimeout(ctx, eff.Timeout)
			ids, err := uploadAttachments(aCtx, resolvedURL, keyValue, paths, eff.PollInterval)
			cancel()
			if err != nil {
				setError(cr, "upload attachments: "+err.Error())
				return cr
			}
			req.AttachmentIDs = ids
		}
	}

	prices := rc.pricingFor(ctx, resolvedURL)

	passed := 0
	for i := 1; i <= repeat; i++ {
		rr := rc.runOnce(ctx, c, tg, req, eff, golden, resolvedURL, prices, i)
		cr.Runs = append(cr.Runs, rr)
		if rr.Status == StatusPass {
			passed++
		}
		rc.emit(Event{Type: EventRunDone, Case: c.Name, Run: i, Msg: string(rr.Status)})

		// Early stop once the min-pass outcome is decided (repeat only).
		if !opts.UpdateGolden && repeat > 1 {
			if passed >= required {
				break
			}
			if (i - passed) > (repeat - required) {
				break
			}
		}
	}
	cr.PassedRuns = passed
	cr.Status = decideCaseStatus(cr.Runs, passed, required)
	return cr
}

// runOnce performs one full run of a case (every turn), evaluates assertions
// (and the judge), and optionally records a golden answer.
func (rc *runContext) runOnce(ctx context.Context, c *spec.Case, tg target.Target, base target.Request, eff spec.Effective, golden *spec.Golden, resolvedURL string, prices *pricing.Table, index int) *RunResult {
	rr := &RunResult{Index: index}
	questions := c.Questions()
	multi := c.MultiTurn()

	var (
		res         *target.Result // last turn's result
		history     []target.Exchange
		convID      string
		total       time.Duration
		firstOutput time.Duration
		usage       *target.Usage
		turnAsserts []assert.Result
	)
	runStart := time.Now()
	for ti, q := range questions {
		req := base
		req.Question = q
		req.ConversationID = convID
		req.History = history
		// Attachments accompany the first turn of conversation-carrying
		// targets; stateless replay (v1) needs them on every turn.
		if ti > 0 && eff.Target != spec.TargetV1 {
			req.AttachmentIDs = nil
			req.InlineFiles = nil
		}
		last := ti == len(questions)-1

		runCtx, cancel := context.WithTimeout(ctx, eff.Timeout)
		tres, err := tg.Run(runCtx, req)
		cancel()
		if err != nil {
			var se *target.ServerError
			if last && c.Expect.Error != nil && errors.As(err, &se) {
				// Negative case: the server error IS the outcome to assert on.
				rr.ErrorResponse = &ErrorResponse{Status: se.Status, Message: se.Message}
				rr.LatencyMS = time.Since(runStart).Milliseconds()
				rr.Assertions = append(turnAsserts, assert.EvaluateError(c.Expect, se, time.Since(runStart))...)
				rr.Status = runStatus(rr.Assertions)
				return rr
			}
			rr.Status = StatusError
			rr.Error = err.Error()
			if multi {
				rr.Error = fmt.Sprintf("turn %d: %s", ti+1, err.Error())
			}
			rr.LatencyMS = time.Since(runStart).Milliseconds()
			return rr
		}

		res = tres
		total += tres.Latency
		if firstOutput == 0 && tres.FirstOutput > 0 {
			firstOutput = tres.FirstOutput
		}
		usage = addUsage(usage, tres.Usage)
		if tres.ConversationID != "" {
			convID = tres.ConversationID
		}
		history = append(history, target.Exchange{Question: q, Answer: tres.Answer})

		if multi {
			tr := &TurnResult{
				Index:          ti + 1,
				Question:       q,
				Answer:         tres.Answer,
				ConversationID: tres.ConversationID,
				LatencyMS:      tres.Latency.Milliseconds(),
				FirstOutputMS:  tres.FirstOutput.Milliseconds(),
				Usage:          tres.Usage,
			}
			if te := c.Turns[ti].Expect; te != nil && !te.Empty() {
				tr.Assertions = assert.Evaluate(*te, tres, nil)
				if te.Judge != nil && !rc.opts.UpdateGolden {
					jr, _ := rc.evaluateJudge(ctx, c, history, tres.Answer, te.Judge, eff, resolvedURL)
					tr.Assertions = append(tr.Assertions, jr)
				}
				for _, a := range tr.Assertions {
					a.Name = fmt.Sprintf("turn %d: %s", ti+1, a.Name)
					turnAsserts = append(turnAsserts, a)
				}
			}
			rr.Turns = append(rr.Turns, tr)
		}
	}

	// Case-level expect applies to the last turn's answer. Whole-run latency
	// (all turns) drives limits.max_seconds; TTFT is the first turn's.
	final := *res
	final.Latency = total
	final.FirstOutput = firstOutput
	final.Usage = usage

	rr.Answer = res.Answer
	rr.Usage = usage
	rr.LatencyMS = total.Milliseconds()
	rr.FirstOutputMS = firstOutput.Milliseconds()
	rr.SourceCount = len(res.Sources)
	rr.ToolCalls = toolCallNames(res.ToolCalls)
	rr.Frames = res.Frames
	if usage != nil {
		model := firstNonEmpty(eff.Model, prices.DefaultModelID())
		if cost, ok := prices.Cost(model, usage.PromptTokens, usage.CompletionTokens); ok {
			rr.CostUSD = &cost
		}
	}

	if c.Expect.Error != nil {
		// Success is the wrong outcome for a negative case.
		rr.Assertions = append(turnAsserts, assert.UnexpectedSuccess(res.Answer))
		rr.Status = StatusFail
		return rr
	}

	assertions := append(turnAsserts, assert.Evaluate(c.Expect, &final, golden)...)
	if c.Expect.Judge != nil && !rc.opts.UpdateGolden {
		jr, verdict := rc.evaluateJudge(ctx, c, history, res.Answer, c.Expect.Judge, eff, resolvedURL)
		assertions = append(assertions, jr)
		rr.Judge = verdict
	}

	if rc.opts.UpdateGolden {
		if err := c.SaveGolden(&spec.Golden{Answer: res.Answer}); err != nil {
			// Recording is the whole point of this mode: a write failure is a
			// run error (assertion failures stay informational in record mode).
			rr.Assertions = assertions
			rr.Status = StatusError
			rr.Error = "record golden " + c.GoldenPath() + ": " + err.Error()
			return rr
		}
		rc.emit(Event{Type: EventGolden, Case: c.Name, Msg: c.GoldenPath()})
	}

	rr.Assertions = assertions
	rr.Status = runStatus(assertions)
	return rr
}

// evaluateJudge grades answer with the resolved judge agent and returns a
// single assert.Result named "judge" plus the recorded verdict (nil on
// error). For multi-turn cases the judge sees the whole transcript so far.
func (rc *runContext) evaluateJudge(ctx context.Context, c *spec.Case, history []target.Exchange, answer string, je *spec.JudgeExpect, eff spec.Effective, resolvedURL string) (assert.Result, *JudgeResult) {
	const name = "judge"
	opts := rc.opts
	if opts.Judge == nil {
		return assert.Result{Name: name, Status: assert.StatusFail,
			Message: "no judge agent configured (bench.yaml judge: or --judge)"}, nil
	}
	keyValue, _, err := opts.ResolveKey(opts.Judge.Agent)
	if err != nil {
		return assert.Result{Name: name, Status: assert.StatusFail,
			Message: "cannot resolve judge agent: " + err.Error()}, nil
	}
	base := firstNonEmpty(opts.Judge.BaseURL, resolvedURL)
	minScore := judge.DefaultMinScore
	if ms := je.MinScore; ms != nil {
		minScore = *ms // an explicit 0 is a legitimate accept-anything threshold
	}

	cfg := judge.Config{
		BaseURL:     base,
		APIKey:      keyValue,
		Timeout:     eff.Timeout,
		Model:       opts.Judge.Model,
		Temperature: opts.Judge.Temperature,
		RunTag:      opts.RunTag,
	}
	verdict, err := judgeRun(ctx, cfg, judgeQuestion(history), answer, je.Rubric)
	if err != nil {
		return assert.Result{Name: name, Status: assert.StatusFail, Message: "judge error: " + err.Error()}, nil
	}

	rec := &JudgeResult{Score: verdict.Score, MinScore: minScore, Reasoning: verdict.Reasoning, Model: opts.Judge.Model}
	msg := fmt.Sprintf("score %.2f (min %.2f)", verdict.Score, minScore)
	if verdict.Reasoning != "" {
		msg += ": " + verdict.Reasoning
	}
	if verdict.Score >= minScore {
		return assert.Result{Name: name, Status: assert.StatusPass, Message: msg}, rec
	}
	return assert.Result{Name: name, Status: assert.StatusFail, Message: msg}, rec
}

// judgeQuestion renders what the judge sees as the "question": the last user
// message for single-turn cases, or the transcript of earlier turns followed
// by the final question for multi-turn cases (so rubrics like "includes the
// decisions from turns 1–5" are gradable).
func judgeQuestion(history []target.Exchange) string {
	if len(history) == 0 {
		return ""
	}
	if len(history) == 1 {
		return history[0].Question
	}
	var b strings.Builder
	b.WriteString("This is a multi-turn conversation. Earlier turns:\n\n")
	for i, ex := range history[:len(history)-1] {
		fmt.Fprintf(&b, "[TURN %d — USER]\n%s\n\n[TURN %d — ASSISTANT]\n%s\n\n", i+1, ex.Question, i+1, ex.Answer)
	}
	fmt.Fprintf(&b, "[FINAL USER MESSAGE — the ANSWER below responds to this]\n%s", history[len(history)-1].Question)
	return b.String()
}

// effective resolves target/model/repeat/min_pass/timeout overrides. Agent and
// base URL precedence is applied per case in runCase (they involve config
// fallback).
func (o Options) effective(c *spec.Case) spec.Effective {
	eff := o.Suite.Effective(c)
	if o.TargetOverride != "" {
		eff.Target = o.TargetOverride
	}
	if o.ModelOverride != "" {
		eff.Model = o.ModelOverride
	}
	if o.WebhookURLOverride != "" {
		eff.WebhookURL = o.WebhookURLOverride
	}
	if o.RepeatOverride > 0 {
		eff.Repeat = o.RepeatOverride
	}
	if o.MinPassOverride > 0 {
		eff.MinPass = o.MinPassOverride
	}
	if o.TimeoutOverride > 0 {
		eff.Timeout = o.TimeoutOverride
	}
	if o.PollIntervalOverride > 0 {
		eff.PollInterval = o.PollIntervalOverride
	}
	if eff.Repeat < 1 {
		eff.Repeat = 1
	}
	if eff.MinPass < 1 {
		eff.MinPass = 1
	}
	if eff.MinPass > eff.Repeat {
		eff.MinPass = eff.Repeat
	}
	return eff
}

// setError marks a case as errored with a single synthetic run carrying msg, so
// reporters can surface the failure uniformly.
func setError(cr *CaseResult, msg string) {
	cr.Status = StatusError
	cr.Runs = append(cr.Runs, &RunResult{Index: 1, Status: StatusError, Error: msg})
	cr.Repeat = 1
	cr.RequiredPass = 1
}

// runStatus is pass unless some assertion failed. Skips never fail a run.
func runStatus(rs []assert.Result) Status {
	for _, a := range rs {
		if a.Status == assert.StatusFail {
			return StatusFail
		}
	}
	return StatusPass
}

// decideCaseStatus folds the runs into a case verdict: error if every executed
// run errored before asserting, otherwise pass/fail on the min-pass threshold.
func decideCaseStatus(runs []*RunResult, passed, required int) Status {
	if len(runs) == 0 {
		return StatusError
	}
	errored := 0
	for _, rr := range runs {
		if rr.Status == StatusError {
			errored++
		}
	}
	if errored == len(runs) {
		return StatusError
	}
	if passed >= required {
		return StatusPass
	}
	return StatusFail
}

func toolCallNames(calls []target.ToolCallInfo) []string {
	if len(calls) == 0 {
		return nil
	}
	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.Name
	}
	return names
}

// addUsage sums usage across turns; nil stays nil until a turn reports usage.
func addUsage(acc, u *target.Usage) *target.Usage {
	if u == nil {
		return acc
	}
	if acc == nil {
		cp := *u
		return &cp
	}
	return &target.Usage{
		PromptTokens:     acc.PromptTokens + u.PromptTokens,
		CompletionTokens: acc.CompletionTokens + u.CompletionTokens,
		TotalTokens:      acc.TotalTokens + u.TotalTokens,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// distinct returns the single value shared by all entries, "mixed" when they
// differ, or "" when there are none.
func distinct(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	seen := vals[0]
	for _, v := range vals[1:] {
		if v != seen {
			return "mixed"
		}
	}
	return seen
}
