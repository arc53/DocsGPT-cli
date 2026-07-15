package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Uses plain net/http (not internal/api) so the judge stays a self-contained
// grading client independent of the interactive chat plumbing.

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Run asks the judge agent to grade answer against rubric for the original
// question and parses a {"score": ..., "reasoning": ...} JSON verdict from its
// reply, tolerating markdown fences and surrounding prose. The score is clamped
// to [0,1]. cfg.Timeout, when > 0, bounds the whole request; ctx cancellation
// is honored throughout.
func Run(ctx context.Context, cfg Config, question, answer, rubric string) (*Verdict, error) {
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	body, err := json.Marshal(chatRequest{
		Model:    "docsgpt",
		Messages: []chatMessage{{Role: "user", Content: buildPrompt(question, answer, rubric)}},
		Stream:   false,
	})
	if err != nil {
		return nil, fmt.Errorf("judge: marshal request: %w", err)
	}

	url := strings.TrimRight(cfg.BaseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("judge: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("judge: request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("judge: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("judge: API error %d: %s", resp.StatusCode, excerpt(string(respBody)))
	}

	var cr chatResponse
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return nil, fmt.Errorf("judge: decode response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("judge: response had no choices")
	}

	verdict, err := parseVerdict(cr.Choices[0].Message.Content)
	if err != nil {
		return nil, fmt.Errorf("judge: %w", err)
	}
	return verdict, nil
}

func buildPrompt(question, answer, rubric string) string {
	var b strings.Builder
	b.WriteString("You are an impartial grader evaluating an AI assistant's answer.\n")
	b.WriteString("Grade how well the ANSWER satisfies the RUBRIC for the given QUESTION.\n\n")
	b.WriteString("[QUESTION]\n")
	b.WriteString(question)
	b.WriteString("\n[END QUESTION]\n\n")
	b.WriteString("[RUBRIC]\n")
	b.WriteString(rubric)
	b.WriteString("\n[END RUBRIC]\n\n")
	b.WriteString("[ANSWER]\n")
	b.WriteString(answer)
	b.WriteString("\n[END ANSWER]\n\n")
	b.WriteString("Respond with ONLY a JSON object, no prose, in exactly this form:\n")
	b.WriteString(`{"score": <number between 0 and 1>, "reasoning": "<one or two sentences>"}` + "\n")
	b.WriteString("where 1 means the answer fully satisfies the rubric and 0 means it fails entirely.")
	return b.String()
}

// parseVerdict extracts and parses the judge's JSON verdict, clamping the score
// into [0,1]. It fails with the raw reply excerpt when nothing parses.
func parseVerdict(reply string) (*Verdict, error) {
	obj, ok := extractJSONObject(reply)
	if !ok {
		return nil, fmt.Errorf("no JSON verdict found in reply: %s", excerpt(strings.TrimSpace(reply)))
	}

	var raw struct {
		Score     json.RawMessage `json:"score"`
		Reasoning string          `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(obj), &raw); err != nil {
		return nil, fmt.Errorf("parse verdict %s: %w", excerpt(obj), err)
	}

	score, err := parseScore(raw.Score)
	if err != nil {
		return nil, fmt.Errorf("%w (reply: %s)", err, excerpt(obj))
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return &Verdict{Score: score, Reasoning: raw.Reasoning}, nil
}

// parseScore reads a score given as a JSON number or a quoted numeric string.
func parseScore(raw json.RawMessage) (float64, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0, fmt.Errorf("verdict has no score")
	}
	if s[0] == '"' { // stringified number, e.g. "0.8"
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			s = strings.TrimSpace(str)
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("verdict score %q is not a number", s)
	}
	return f, nil
}

// extractJSONObject pulls a JSON object out of a reply that may be fenced or
// surrounded by prose: strip a leading ``` fence, then take the first '{' to
// the last '}' and validate it.
func extractJSONObject(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		} else {
			s = strings.TrimPrefix(s, "```")
		}
		s = strings.TrimRight(s, " \t\r\n")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return "", false
	}
	cand := s[start : end+1]
	if !json.Valid([]byte(cand)) {
		return "", false
	}
	return cand, true
}

// excerpt shortens text for error messages.
func excerpt(s string) string {
	const max = 300
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
