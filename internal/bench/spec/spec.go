// Package spec defines the benchmark suite format: a root directory with an
// optional bench.yaml (suite defaults) and any number of case directories,
// each holding a case.yaml plus optional attachment files.
package spec

import (
	"fmt"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// Target names accepted in bench.yaml / case.yaml.
const (
	TargetV1      = "v1"      // POST /v1/chat/completions, Bearer auth
	TargetStream  = "stream"  // POST /stream, api_key in body, SSE
	TargetWebhook = "webhook" // POST webhook URL, poll /api/task_status
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
	Agent        string      `yaml:"agent"`         // key name from ~/.docsgpt/config.json, or a literal API key
	Target       string      `yaml:"target"`        // v1 | stream | webhook
	BaseURL      string      `yaml:"base_url"`      // overrides CLI config base URL
	WebhookURL   string      `yaml:"webhook_url"`   // required for target: webhook
	Judge        *JudgeAgent `yaml:"judge"`         // default judge for expect.judge sections
	Concurrency  int         `yaml:"concurrency"`   // parallel cases; default 1
	Timeout      Duration    `yaml:"timeout"`       // per-run timeout; default 120s
	PollInterval Duration    `yaml:"poll_interval"` // webhook/attachment polling; default 2s
	Repeat       int         `yaml:"repeat"`        // runs per case; default 1
	MinPass      int         `yaml:"min_pass"`      // passing runs required; default = repeat
}

// JudgeAgent identifies the agent used for LLM-as-judge grading.
type JudgeAgent struct {
	Agent   string `yaml:"agent"`    // key name or literal API key
	BaseURL string `yaml:"base_url"` // defaults to the suite base URL
}

// Case is one case.yaml. Zero values defer to the suite config.
type Case struct {
	Name string `yaml:"-"` // relative dir path, set by the loader
	Dir  string `yaml:"-"` // absolute dir path, set by the loader

	Description string     `yaml:"description"`
	Tags        StringList `yaml:"tags"`
	Skip        string     `yaml:"skip"` // non-empty = skip with this reason

	Agent       string     `yaml:"agent"`
	Target      string     `yaml:"target"`
	WebhookURL  string     `yaml:"webhook_url"`
	BaseURL     string     `yaml:"base_url"`
	Question    string     `yaml:"question"`
	Attachments StringList `yaml:"attachments"` // filenames relative to the case dir

	Expect Expect `yaml:"expect"`

	Repeat  int      `yaml:"repeat"`
	MinPass int      `yaml:"min_pass"`
	Timeout Duration `yaml:"timeout"`
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
	Golden  bool           `yaml:"golden"` // compare answer against golden.json (see `bench record`)
}

// Empty reports whether no assertion section is present.
func (e Expect) Empty() bool {
	return e.Answer == nil && len(e.JSON) == 0 && e.Sources == nil &&
		e.Tools == nil && e.Judge == nil && e.Limits == nil && !e.Golden
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
	MaxSeconds     float64 `yaml:"max_seconds"`
	MaxTotalTokens int     `yaml:"max_total_tokens"` // v1 target only (usage not exposed elsewhere)
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
	Agent        string
	Target       string
	BaseURL      string
	WebhookURL   string
	Timeout      time.Duration
	PollInterval time.Duration
	Repeat       int
	MinPass      int
}

// Effective resolves case-level overrides against suite and built-in defaults.
func (s *Suite) Effective(c *Case) Effective {
	eff := Effective{
		Agent:        firstNonEmpty(c.Agent, s.Config.Agent),
		Target:       firstNonEmpty(c.Target, s.Config.Target, TargetV1),
		BaseURL:      firstNonEmpty(c.BaseURL, s.Config.BaseURL),
		WebhookURL:   firstNonEmpty(c.WebhookURL, s.Config.WebhookURL),
		Timeout:      DefaultTimeout,
		PollInterval: DefaultPollInterval,
		Repeat:       1,
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

// Validate checks a case against its effective settings. It is called by the
// loader; callers overriding target/agent at runtime should re-validate.
func (s *Suite) Validate(c *Case) error {
	eff := s.Effective(c)
	switch eff.Target {
	case TargetV1, TargetStream, TargetWebhook:
	default:
		return fmt.Errorf("case %s: unknown target %q (want v1, stream, or webhook)", c.Name, eff.Target)
	}
	if c.Skip != "" {
		return nil
	}
	if c.Question == "" {
		return fmt.Errorf("case %s: question is required", c.Name)
	}
	if c.Expect.Empty() {
		return fmt.Errorf("case %s: expect has no assertion sections", c.Name)
	}
	if eff.Target == TargetWebhook && len(c.Attachments) > 0 {
		return fmt.Errorf("case %s: attachments are not supported by the webhook target", c.Name)
	}
	// A missing webhook_url is NOT rejected here: it can be supplied at run
	// time (--webhook-url). The runner validates it against effective values.
	if c.Expect.Judge != nil && c.Expect.Judge.Rubric == "" {
		return fmt.Errorf("case %s: expect.judge requires a rubric", c.Name)
	}
	if j := c.Expect.Judge; j != nil && j.MinScore != nil && (*j.MinScore < 0 || *j.MinScore > 1) {
		return fmt.Errorf("case %s: expect.judge.min_score must be within [0, 1]", c.Name)
	}
	if c.Timeout < 0 {
		return fmt.Errorf("case %s: timeout must be positive", c.Name)
	}
	if a := c.Expect.Answer; a != nil {
		for _, r := range append(append(StringList{}, a.Regex...), a.NotRegex...) {
			if _, err := regexp.Compile(r); err != nil {
				return fmt.Errorf("case %s: bad regex %q: %w", c.Name, r, err)
			}
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
