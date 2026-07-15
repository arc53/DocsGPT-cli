// Package judge grades an answer with a second agent (LLM-as-judge) through
// the OpenAI-compatible /v1/chat/completions endpoint. The Run entry point and
// the verdict parser live in run.go.
package judge

import "time"

// DefaultMinScore is applied when expect.judge.min_score is unset.
const DefaultMinScore = 0.7

// Config identifies the judge agent.
type Config struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

// Verdict is the judge's parsed grading.
type Verdict struct {
	Score     float64 `json:"score"` // 0..1
	Reasoning string  `json:"reasoning"`
}
