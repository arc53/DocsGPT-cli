package target

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// UploadAttachments uploads each file to POST {baseURL}/api/store_attachment
// (multipart, with the agent api_key as a form field) and returns the resulting
// server-side attachment ids in the same order as paths. When an upload returns
// a task instead of a direct id, it polls /api/task_status until the id is
// ready. One file is sent per request for simplicity.
func UploadAttachments(ctx context.Context, baseURL, apiKey string, paths []string, pollInterval time.Duration) ([]string, error) {
	return uploadAll(ctx, baseURL, attachmentAuth{apiKey: apiKey}, paths, pollInterval)
}

// UploadAttachmentsWithToken is UploadAttachments for runs that address the
// agent by id: there is no agent api_key, so the upload is authenticated with
// the personal access token as the Bearer credential and the attachment is
// owned by the token's user.
func UploadAttachmentsWithToken(ctx context.Context, baseURL, token string, paths []string, pollInterval time.Duration) ([]string, error) {
	return uploadAll(ctx, baseURL, attachmentAuth{token: token}, paths, pollInterval)
}

// attachmentAuth is how one upload authenticates: an agent api_key form field
// or a Bearer personal access token.
type attachmentAuth struct {
	apiKey string
	token  string
}

func uploadAll(ctx context.Context, baseURL string, auth attachmentAuth, paths []string, pollInterval time.Duration) ([]string, error) {
	ids := make([]string, 0, len(paths))
	for _, p := range paths {
		id, err := uploadAttachment(ctx, baseURL, auth, p, pollInterval)
		if err != nil {
			return nil, fmt.Errorf("upload attachment %s: %w", p, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func uploadAttachment(ctx context.Context, baseURL string, auth attachmentAuth, path string, pollInterval time.Duration) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", fmt.Errorf("copy file body: %w", err)
	}
	if auth.apiKey != "" {
		if err := mw.WriteField("api_key", auth.apiKey); err != nil {
			return "", fmt.Errorf("write api_key field: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("finalize multipart body: %w", err)
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/api/store_attachment"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	if auth.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+auth.token)
	}
	setBenchHeaders(httpReq, "")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", endpoint, err)
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return "", fmt.Errorf("read response from %s: %w", endpoint, readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %d: %s", endpoint, resp.StatusCode, truncateBody(body, 300))
	}

	directID := gjson.GetBytes(body, "attachment_id").String()
	taskID := gjson.GetBytes(body, "task_id").String()
	// Multi variant: tasks[0].{attachment_id|task_id}.
	if tasks := gjson.GetBytes(body, "tasks"); tasks.IsArray() {
		first := tasks.Get("0")
		if directID == "" {
			directID = first.Get("attachment_id").String()
		}
		if taskID == "" {
			taskID = first.Get("task_id").String()
		}
	}

	// The attachment id can be handed out before the content-extraction task
	// has run; sending the question at that point races the parser. Whenever a
	// task is reported, wait for it to finish even if an id is already known.
	if taskID != "" {
		polled, err := pollAttachmentID(ctx, baseURL, taskID, pollInterval, auth.token)
		if err != nil {
			return "", err
		}
		if polled != "" {
			return polled, nil
		}
		if directID != "" {
			return directID, nil
		}
		return "", fmt.Errorf("task %s succeeded without an attachment_id", taskID)
	}
	if directID != "" {
		return directID, nil
	}
	return "", fmt.Errorf("%s response has no attachment_id or task_id: %s", endpoint, truncateBody(body, 300))
}

// pollAttachmentID polls task_status until SUCCESS and returns the attachment
// id from the task result ("" when the result carries none).
func pollAttachmentID(ctx context.Context, baseURL, taskID string, pollInterval time.Duration, token string) (string, error) {
	statusBody, err := pollTaskStatus(ctx, baseURL, taskID, pollInterval, token)
	if err != nil {
		return "", err
	}
	id := gjson.GetBytes(statusBody, "result.attachment_id").String()
	if id == "" {
		// Some deployments nest the task return one level deeper.
		id = gjson.GetBytes(statusBody, "result.result.attachment_id").String()
	}
	return id, nil
}
