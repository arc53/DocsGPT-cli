// Package spec defines the benchmark suite format: a root directory with an
// optional bench.yaml (suite defaults) and any number of case directories,
// each holding a case.yaml plus optional attachment files.
package spec

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Target names accepted in bench.yaml / case.yaml.
const (
	TargetV1      = "v1"      // POST /v1/chat/completions, Bearer auth
	TargetStream  = "stream"  // POST /stream, api_key in body, SSE
	TargetAnswer  = "answer"  // POST /api/answer, api_key in body, JSON
	TargetWebhook = "webhook" // POST webhook URL, poll /api/task_status
)

// AllTargets lists the accepted target names in display order.
var AllTargets = []string{TargetV1, TargetStream, TargetAnswer, TargetWebhook}

// Attachment modes accepted in bench.yaml / case.yaml.
const (
	AttachmentsUpload = "upload" // POST /api/store_attachment, send ids (default)
	AttachmentsInline = "inline" // v1 only: base64 file content parts in the message
)

// Values accepted by expect.stream.thought.
const (
	ThoughtIgnore  = "ignore"
	ThoughtPresent = "present"
	ThoughtAbsent  = "absent"
)

// Defaults applied when neither the case nor the suite sets a value.
const (
	DefaultTimeout      = 120 * time.Second
	DefaultPollInterval = 2 * time.Second
)

// Duration accepts "90s"/"2m" strings or bare numbers (seconds) in YAML.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var num float64
	if err := node.Decode(&num); err == nil {
		*d = Duration(time.Duration(num * float64(time.Second)))
		return nil
	}
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("invalid duration %q", node.Value)
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(dur)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// StringList accepts a single string or a list of strings in YAML.
type StringList []string

func (s *StringList) UnmarshalYAML(node *yaml.Node) error {
	var list []string
	var single string
	if err := node.Decode(&single); err == nil {
		list = []string{single}
	} else if err := node.Decode(&list); err != nil {
		return err
	}
	// Drop empty entries: `contains: ""` would otherwise create a check that
	// always passes (or an empty tag), which is never what the author meant.
	out := list[:0]
	for _, v := range list {
		if v != "" {
			out = append(out, v)
		}
	}
	*s = StringList(out)
	return nil
}

// SuiteConfig is bench.yaml at the suite root: defaults for every case.
type SuiteConfig struct {
	Agent           string      `yaml:"agent"`            // key name from ~/.docsgpt/config.json, or a literal API key
	Target          string      `yaml:"target"`           // v1 | stream | answer | webhook
	Model           string      `yaml:"model"`            // model id sent with every request (empty = agent default)
	Stream          *bool       `yaml:"stream"`           // v1 target: SSE streaming instead of one JSON response
	AttachmentsMode string      `yaml:"attachments_mode"` // upload (default) | inline (v1 only)
	BaseURL         string      `yaml:"base_url"`         // overrides CLI config base URL
	WebhookURL      string      `yaml:"webhook_url"`      // required for target: webhook
	Judge           *JudgeAgent `yaml:"judge"`            // default judge for expect.judge sections
	Concurrency     int         `yaml:"concurrency"`      // parallel cases; default 1
	Timeout         Duration    `yaml:"timeout"`          // per-run (per-turn) timeout; default 120s
	PollInterval    Duration    `yaml:"poll_interval"`    // webhook/attachment polling; default 2s
	Repeat          int         `yaml:"repeat"`           // runs per case; default 1
	MinPass         int         `yaml:"min_pass"`         // passing runs required; default = repeat
	RunTag          string      `yaml:"run_tag"`          // sent as X-DocsGPT-Bench-Tag: bench:<tag> (see --run-tag)

	// Pricing overrides/fallbacks for the cost column, keyed by model id.
	// Used when /api/models does not expose pricing for a model.
	Pricing map[string]ModelPricing `yaml:"pricing"`
}

// ModelPricing is the USD price per one million tokens.
type ModelPricing struct {
	InputPerMillion  float64 `yaml:"input_per_million" json:"input_per_million"`
	OutputPerMillion float64 `yaml:"output_per_million" json:"output_per_million"`
}

// JudgeAgent identifies the agent used for LLM-as-judge grading.
type JudgeAgent struct {
	Agent       string   `yaml:"agent"`       // key name or literal API key
	BaseURL     string   `yaml:"base_url"`    // defaults to the suite base URL
	Model       string   `yaml:"model"`       // model id forwarded in the judge chat request
	Temperature *float64 `yaml:"temperature"` // sampling temperature passthrough (e.g. 0)
}

// Turn is one user message of a multi-turn case.
type Turn struct {
	Question string  `yaml:"question"`
	Expect   *Expect `yaml:"expect"` // optional per-turn assertions
}

// Case is one case.yaml. Zero values defer to the suite config.
type Case struct {
	Name string `yaml:"-"` // relative dir path, set by the loader
	Dir  string `yaml:"-"` // absolute dir path, set by the loader

	Description string     `yaml:"description"`
	Tags        StringList `yaml:"tags"`
	Skip        string     `yaml:"skip"` // non-empty = skip with this reason

	Agent           string     `yaml:"agent"`
	Target          string     `yaml:"target"`
	Model           string     `yaml:"model"`
	Stream          *bool      `yaml:"stream"`
	AttachmentsMode string     `yaml:"attachments_mode"`
	WebhookURL      string     `yaml:"webhook_url"`
	BaseURL         string     `yaml:"base_url"`
	Question        string     `yaml:"question"`
	Turns           []Turn     `yaml:"turns"`       // multi-turn alternative to question
	Attachments     StringList `yaml:"attachments"` // filenames relative to the case dir

	Expect Expect `yaml:"expect"` // applies to the (last) answer

	Repeat  int      `yaml:"repeat"`
	MinPass int      `yaml:"min_pass"`
	Timeout Duration `yaml:"timeout"`
}

// MultiTurn reports whether the case is defined by turns rather than question.
func (c *Case) MultiTurn() bool { return len(c.Turns) > 0 }

// Questions returns the ordered user messages of the case.
func (c *Case) Questions() []string {
	if !c.MultiTurn() {
		return []string{c.Question}
	}
	out := make([]string, len(c.Turns))
	for i, t := range c.Turns {
		out[i] = t.Question
	}
	return out
}

// anyTurnExpect reports whether at least one turn carries assertions.
func (c *Case) anyTurnExpect() bool {
	for _, t := range c.Turns {
		if t.Expect != nil && !t.Expect.Empty() {
			return true
		}
	}
	return false
}

// FinalQuestion returns the last user message (the one expect applies to).
func (c *Case) FinalQuestion() string {
	qs := c.Questions()
	return qs[len(qs)-1]
}

// Expect holds the assertion sections. Every section is optional; omitted
// sections are skipped, mirroring the original expected.yaml behavior.
type Expect struct {
	Answer  *AnswerExpect  `yaml:"answer"`
	JSON    map[string]any `yaml:"json"` // gjson path -> matcher (scalar or matcher map)
	Sources *SourcesExpect `yaml:"sources"`
	Tools   *ToolsExpect   `yaml:"tools"`
	Judge   *JudgeExpect   `yaml:"judge"`
	Limits  *LimitsExpect  `yaml:"limits"`
	Stream  *StreamExpect  `yaml:"stream"` // SSE integrity (stream target)
	Error   *ErrorExpect   `yaml:"error"`  // negative case: a server error is the expected outcome
	Golden  bool           `yaml:"golden"` // compare answer against golden.json (see `bench record`)
}

// Empty reports whether no assertion section is present.
func (e Expect) Empty() bool {
	return e.Answer == nil && len(e.JSON) == 0 && e.Sources == nil &&
		e.Tools == nil && e.Judge == nil && e.Limits == nil && e.Stream == nil &&
		e.Error == nil && !e.Golden
}

// hasAnswerSections reports whether any section that needs a successful
// answer is present (everything except error and limits).
func (e Expect) hasAnswerSections() bool {
	return e.Answer != nil || len(e.JSON) > 0 || e.Sources != nil ||
		e.Tools != nil || e.Judge != nil || e.Stream != nil || e.Golden
}

// AnswerExpect asserts on the final answer text.
type AnswerExpect struct {
	Equals      string     `yaml:"equals"`
	Contains    StringList `yaml:"contains"`     // case-insensitive substrings
	NotContains StringList `yaml:"not_contains"` // case-insensitive substrings
	Regex       StringList `yaml:"regex"`
	NotRegex    StringList `yaml:"not_regex"`
}

// SourcesExpect asserts on the number of retrieval sources returned.
type SourcesExpect struct {
	Min *int `yaml:"min"`
	Max *int `yaml:"max"`
}

// ToolsExpect asserts on tool calls made by the agent.
type ToolsExpect struct {
	Called    StringList `yaml:"called"`
	NotCalled StringList `yaml:"not_called"`
}

// JudgeExpect grades the answer with a second agent (LLM-as-judge).
type JudgeExpect struct {
	Rubric   string   `yaml:"rubric"`
	MinScore *float64 `yaml:"min_score"` // 0..1; nil = judge.DefaultMinScore (explicit 0 is honored)
}

// LimitsExpect asserts performance/cost budgets.
type LimitsExpect struct {
	MaxSeconds           float64 `yaml:"max_seconds"`             // whole run (all turns)
	MaxFirstTokenSeconds float64 `yaml:"max_first_token_seconds"` // time to first output (stream / v1 streamed)
	MaxTotalTokens       int     `yaml:"max_total_tokens"`        // v1 target only (usage not exposed elsewhere)
}

// StreamExpect asserts on SSE stream integrity (stream target only).
type StreamExpect struct {
	EndFrame      *bool      `yaml:"end_frame"`      // an `end` frame (or [DONE]) was observed
	ErrorFrame    *bool      `yaml:"error_frame"`    // an `error` frame was observed (only false is meaningful)
	Thought       string     `yaml:"thought"`        // present | absent | ignore (default)
	FramesContain StringList `yaml:"frames_contain"` // frame types that must appear at least once
}

// ErrorExpect makes a server error the expected outcome. When present, a
// successful answer fails the case.
type ErrorExpect struct {
	Status   int        `yaml:"status"`   // HTTP status; 0 = any
	Contains StringList `yaml:"contains"` // case-insensitive substrings of the error message/body
}

// Suite is a fully loaded benchmark directory.
type Suite struct {
	Dir    string // absolute path of the suite root
	Name   string // base name of the root dir, used for run history
	Config SuiteConfig
	Cases  []*Case
}

// Effective is a case's settings after suite defaults and built-in defaults.
type Effective struct {
	Agent           string
	Target          string
	Model           string
	Stream          bool // v1 SSE mode
	AttachmentsMode string
	BaseURL         string
	WebhookURL      string
	Timeout         time.Duration
	PollInterval    time.Duration
	Repeat          int
	MinPass         int
}

// Effective resolves case-level overrides against suite and built-in defaults.
func (s *Suite) Effective(c *Case) Effective {
	eff := Effective{
		Agent:           firstNonEmpty(c.Agent, s.Config.Agent),
		Target:          firstNonEmpty(c.Target, s.Config.Target, TargetV1),
		Model:           firstNonEmpty(c.Model, s.Config.Model),
		AttachmentsMode: firstNonEmpty(c.AttachmentsMode, s.Config.AttachmentsMode, AttachmentsUpload),
		BaseURL:         firstNonEmpty(c.BaseURL, s.Config.BaseURL),
		WebhookURL:      firstNonEmpty(c.WebhookURL, s.Config.WebhookURL),
		Timeout:         DefaultTimeout,
		PollInterval:    DefaultPollInterval,
		Repeat:          1,
	}
	if s.Config.Stream != nil {
		eff.Stream = *s.Config.Stream
	}
	if c.Stream != nil {
		eff.Stream = *c.Stream
	}
	if s.Config.Timeout != 0 {
		eff.Timeout = s.Config.Timeout.Std()
	}
	if c.Timeout != 0 {
		eff.Timeout = c.Timeout.Std()
	}
	if s.Config.PollInterval != 0 {
		eff.PollInterval = s.Config.PollInterval.Std()
	}
	if s.Config.Repeat > 0 {
		eff.Repeat = s.Config.Repeat
	}
	if c.Repeat > 0 {
		eff.Repeat = c.Repeat
	}
	eff.MinPass = eff.Repeat
	if s.Config.MinPass > 0 {
		eff.MinPass = s.Config.MinPass
	}
	if c.MinPass > 0 {
		eff.MinPass = c.MinPass
	}
	if eff.MinPass > eff.Repeat {
		eff.MinPass = eff.Repeat
	}
	return eff
}

// ValidTarget reports whether name is a known target.
func ValidTarget(name string) bool {
	for _, t := range AllTargets {
		if t == name {
			return true
		}
	}
	return false
}

// Validate checks a case against its effective settings. It is called by the
// loader; callers overriding target/agent at runtime should re-validate.
func (s *Suite) Validate(c *Case) error {
	eff := s.Effective(c)
	if !ValidTarget(eff.Target) {
		return fmt.Errorf("case %s: unknown target %q (want %s)", c.Name, eff.Target, strings.Join(AllTargets, ", "))
	}
	if c.Skip != "" {
		return nil
	}
	if c.Question == "" && !c.MultiTurn() {
		return fmt.Errorf("case %s: question (or turns) is required", c.Name)
	}
	if c.Question != "" && c.MultiTurn() {
		return fmt.Errorf("case %s: question and turns are mutually exclusive", c.Name)
	}
	for i, t := range c.Turns {
		if strings.TrimSpace(t.Question) == "" {
			return fmt.Errorf("case %s: turns[%d].question is required", c.Name, i)
		}
		if t.Expect != nil {
			if t.Expect.Error != nil && i < len(c.Turns)-1 {
				return fmt.Errorf("case %s: turns[%d]: expect.error is only allowed on the last turn (use the case-level expect)", c.Name, i)
			}
			if err := validateExpect(c.Name, fmt.Sprintf("turns[%d].", i), t.Expect); err != nil {
				return err
			}
		}
	}
	if c.MultiTurn() && eff.Target == TargetWebhook {
		return fmt.Errorf("case %s: turns are not supported by the webhook target (it never continues a conversation)", c.Name)
	}
	if c.Expect.Empty() && !c.anyTurnExpect() {
		return fmt.Errorf("case %s: expect has no assertion sections", c.Name)
	}
	if err := validateExpect(c.Name, "", &c.Expect); err != nil {
		return err
	}
	if len(c.Attachments) > 0 && (eff.Target == TargetWebhook || eff.Target == TargetAnswer) {
		return fmt.Errorf("case %s: attachments are not supported by the %s target", c.Name, eff.Target)
	}
	switch eff.AttachmentsMode {
	case AttachmentsUpload:
	case AttachmentsInline:
		if eff.Target != TargetV1 {
			return fmt.Errorf("case %s: attachments_mode inline requires target v1 (got %s)", c.Name, eff.Target)
		}
	default:
		return fmt.Errorf("case %s: unknown attachments_mode %q (want %s or %s)", c.Name, eff.AttachmentsMode, AttachmentsUpload, AttachmentsInline)
	}
	if c.Expect.Stream != nil && eff.Target != TargetStream {
		return fmt.Errorf("case %s: expect.stream requires target stream (got %s)", c.Name, eff.Target)
	}
	// A missing webhook_url is NOT rejected here: it can be supplied at run
	// time (--webhook-url). The runner validates it against effective values.
	if c.Timeout < 0 {
		return fmt.Errorf("case %s: timeout must be positive", c.Name)
	}
	return nil
}

// validateExpect checks the static shape of one expect block. prefix labels
// per-turn blocks in error messages.
func validateExpect(caseName, prefix string, e *Expect) error {
	if e.Judge != nil && e.Judge.Rubric == "" {
		return fmt.Errorf("case %s: %sexpect.judge requires a rubric", caseName, prefix)
	}
	if j := e.Judge; j != nil && j.MinScore != nil && (*j.MinScore < 0 || *j.MinScore > 1) {
		return fmt.Errorf("case %s: %sexpect.judge.min_score must be within [0, 1]", caseName, prefix)
	}
	if a := e.Answer; a != nil {
		for _, r := range append(append(StringList{}, a.Regex...), a.NotRegex...) {
			if _, err := regexp.Compile(r); err != nil {
				return fmt.Errorf("case %s: %sbad regex %q: %w", caseName, prefix, r, err)
			}
		}
	}
	if st := e.Stream; st != nil {
		switch st.Thought {
		case "", ThoughtIgnore, ThoughtPresent, ThoughtAbsent:
		default:
			return fmt.Errorf("case %s: %sexpect.stream.thought must be present, absent, or ignore (got %q)", caseName, prefix, st.Thought)
		}
		if st.ErrorFrame != nil && *st.ErrorFrame {
			return fmt.Errorf("case %s: %sexpect.stream.error_frame: true is not supported — use expect.error for negative cases", caseName, prefix)
		}
	}
	if e.Error != nil {
		if e.hasAnswerSections() {
			return fmt.Errorf("case %s: %sexpect.error cannot be combined with answer/json/sources/tools/judge/stream/golden (a successful answer fails an error case)", caseName, prefix)
		}
		if e.Error.Status < 0 || (e.Error.Status > 0 && (e.Error.Status < 100 || e.Error.Status > 599)) {
			return fmt.Errorf("case %s: %sexpect.error.status must be an HTTP status code", caseName, prefix)
		}
	}
	if l := e.Limits; l != nil {
		if l.MaxSeconds < 0 || l.MaxFirstTokenSeconds < 0 || l.MaxTotalTokens < 0 {
			return fmt.Errorf("case %s: %sexpect.limits values must be positive", caseName, prefix)
		}
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
