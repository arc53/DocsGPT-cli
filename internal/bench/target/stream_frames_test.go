package target

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A workflow agent streams its steps; with long documents one workflow_step
// frame can be several MiB.
func bigStepFrame(size int) string {
	return `data: {"type":"workflow_step","node_id":"read","status":"completed","state_delta":{"text":"` +
		strings.Repeat("x", size) + `"}}`
}

func serveStream(t *testing.T, frames ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, strings.Join(frames, "\n\n")+"\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withFrameLimit(t *testing.T, limit int) {
	t.Helper()
	old := maxFrameBytes
	maxFrameBytes = limit
	t.Cleanup(func() { maxFrameBytes = old })
}

func TestStreamTargetSkipsAnOversizedStepFrameAndKeepsReading(t *testing.T) {
	withFrameLimit(t, 1<<20)
	srv := serveStream(t,
		`data: {"type":"workflow_run","workflow_run_id":"run-42","step_total":2}`,
		`data: {"type":"answer","answer":"Delivery is "}`,
		bigStepFrame(5<<20),
		`data: {"type":"answer","answer":"30 days."}`,
		`data: [DONE]`,
	)

	res, err := streamTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("a large step frame must not abort the case: %v", err)
	}
	if res.Answer != "Delivery is 30 days." {
		t.Errorf("Answer = %q", res.Answer)
	}
	if !res.EndFrame {
		t.Error("the [DONE] after the skipped frame should still be read")
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "workflow_step") {
		t.Errorf("want one skipped-frame warning naming the frame, got %q", res.Warnings)
	}
	if res.WorkflowRunID != "run-42" {
		t.Errorf("WorkflowRunID = %q", res.WorkflowRunID)
	}
}

func TestStreamTargetReadsAMultiMegabyteFrameUnderTheLimit(t *testing.T) {
	srv := serveStream(t,
		`data: {"type":"answer","answer":"ok"}`,
		bigStepFrame(5<<20), // over the old 1 MiB scanner cap, under the default limit
		`data: {"type":"end"}`,
	)

	res, err := streamTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("stream Run: %v", err)
	}
	if res.Answer != "ok" || !res.EndFrame || len(res.Warnings) != 0 {
		t.Errorf("answer=%q end=%v warnings=%q", res.Answer, res.EndFrame, res.Warnings)
	}
}

func TestStreamTargetFailsWhenAFrameItNeedsIsTooLarge(t *testing.T) {
	withFrameLimit(t, 1<<20)
	for _, frame := range []string{
		`data: {"type":"answer","answer":"` + strings.Repeat("a", 2<<20) + `"}`,
		`data: {"type":"error","error":"` + strings.Repeat("e", 2<<20) + `"}`,
		`data: {"type":"end","pad":"` + strings.Repeat("p", 2<<20) + `"}`,
		`data: {"answer":"` + strings.Repeat("u", 2<<20) + `"}`, // type can't be told
	} {
		srv := serveStream(t, `data: {"type":"answer","answer":"a"}`, frame, `data: [DONE]`)
		_, err := streamTarget{}.Run(context.Background(), Request{Question: "q", BaseURL: srv.URL, APIKey: "k"})
		if err == nil || !strings.Contains(err.Error(), "limit") {
			t.Errorf("frame %.40s…: want a too-large error, got %v", frame, err)
		}
	}
}
