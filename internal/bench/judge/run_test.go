package judge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// completionServer returns an httptest server whose /v1/chat/completions reply
// carries content as the assistant message. reqCheck, if set, inspects the
// incoming request body.
func completionServer(t *testing.T, content string, status int, delay time.Duration, reqCheck func(*testing.T, chatRequest, *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if reqCheck != nil {
			body, _ := io.ReadAll(r.Body)
			var cr chatRequest
			if err := json.Unmarshal(body, &cr); err != nil {
				t.Errorf("decode request: %v", err)
			}
			reqCheck(t, cr, r)
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			io.WriteString(w, `{"error":"boom"}`)
			return
		}
		resp := map[string]any{
			"choices": []any{
				map[string]any{"message": map[string]any{"content": content}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestRunCleanVerdict(t *testing.T) {
	srv := completionServer(t, `{"score": 0.9, "reasoning": "solid answer"}`, http.StatusOK, 0,
		func(t *testing.T, cr chatRequest, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
				t.Errorf("auth header = %q", got)
			}
			if cr.Stream {
				t.Errorf("stream must be false")
			}
			if len(cr.Messages) != 1 || cr.Messages[0].Role != "user" {
				t.Errorf("expected one user message, got %+v", cr.Messages)
			}
			if !strings.Contains(cr.Messages[0].Content, "grade this rubric") {
				t.Errorf("prompt missing rubric: %q", cr.Messages[0].Content)
			}
		})
	defer srv.Close()

	v, err := Run(context.Background(), Config{BaseURL: srv.URL, APIKey: "secret-key"},
		"how long?", "about 45 minutes", "grade this rubric")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v.Score != 0.9 || v.Reasoning != "solid answer" {
		t.Errorf("verdict = %+v", v)
	}
}

func TestRunFencedVerdict(t *testing.T) {
	srv := completionServer(t, "```json\n{\"score\": 0.5, \"reasoning\": \"partial\"}\n```", http.StatusOK, 0, nil)
	defer srv.Close()

	v, err := Run(context.Background(), Config{BaseURL: srv.URL, APIKey: "k"}, "q", "a", "r")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v.Score != 0.5 {
		t.Errorf("score = %v, want 0.5", v.Score)
	}
}

func TestRunVerdictWrappedInProse(t *testing.T) {
	srv := completionServer(t, `Sure, here is my grade: {"score": 1, "reasoning": "great"} Hope this helps!`, http.StatusOK, 0, nil)
	defer srv.Close()

	v, err := Run(context.Background(), Config{BaseURL: srv.URL, APIKey: "k"}, "q", "a", "r")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v.Score != 1 || v.Reasoning != "great" {
		t.Errorf("verdict = %+v", v)
	}
}

func TestRunScoreClamped(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    float64
	}{
		{`{"score": 1.5, "reasoning": "x"}`, 1},
		{`{"score": -0.3, "reasoning": "x"}`, 0},
		{`{"score": "0.42", "reasoning": "stringy"}`, 0.42},
	} {
		srv := completionServer(t, tc.content, http.StatusOK, 0, nil)
		v, err := Run(context.Background(), Config{BaseURL: srv.URL, APIKey: "k"}, "q", "a", "r")
		srv.Close()
		if err != nil {
			t.Fatalf("Run(%q): %v", tc.content, err)
		}
		if v.Score != tc.want {
			t.Errorf("content %q: score = %v, want %v", tc.content, v.Score, tc.want)
		}
	}
}

func TestRunNonParseableReply(t *testing.T) {
	srv := completionServer(t, "I cannot grade this answer.", http.StatusOK, 0, nil)
	defer srv.Close()

	_, err := Run(context.Background(), Config{BaseURL: srv.URL, APIKey: "k"}, "q", "a", "r")
	if err == nil {
		t.Fatal("expected error for non-parseable reply")
	}
	if !strings.Contains(err.Error(), "no JSON verdict") || !strings.Contains(err.Error(), "cannot grade") {
		t.Errorf("error should include reason and raw excerpt: %v", err)
	}
}

func TestRunHTTP500(t *testing.T) {
	srv := completionServer(t, "", http.StatusInternalServerError, 0, nil)
	defer srv.Close()

	_, err := Run(context.Background(), Config{BaseURL: srv.URL, APIKey: "k"}, "q", "a", "r")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want status code", err)
	}
}

func TestRunContextTimeout(t *testing.T) {
	srv := completionServer(t, `{"score":1,"reasoning":"x"}`, http.StatusOK, 200*time.Millisecond, nil)
	defer srv.Close()

	start := time.Now()
	_, err := Run(context.Background(), Config{BaseURL: srv.URL, APIKey: "k", Timeout: 20 * time.Millisecond},
		"q", "a", "r")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 150*time.Millisecond {
		t.Errorf("timeout did not fire promptly: %v", time.Since(start))
	}
}

func TestRunCanceledContext(t *testing.T) {
	srv := completionServer(t, `{"score":1,"reasoning":"x"}`, http.StatusOK, 200*time.Millisecond, nil)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, Config{BaseURL: srv.URL, APIKey: "k"}, "q", "a", "r"); err == nil {
		t.Fatal("expected error for canceled context")
	}
}
