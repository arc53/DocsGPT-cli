package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/display"
)

func (s *chatSession) rateGood(comment string) { s.rate("like", comment) }
func (s *chatSession) rateBad(comment string)  { s.rate("dislike", comment) }

// rate sends feedback on the last answer to the server, which keeps it
// with the answer in its conversation, for the agent's owner. The server
// counts a conversation's answers from 0 (Turn.Index) and takes only
// "like" or "dislike", no comment.
func (s *chatSession) rate(feedback, comment string) {
	turns := s.sess.Turns()
	if len(turns) == 0 {
		s.fail("No answer to rate yet.")
		return
	}
	t := turns[len(turns)-1]
	if t.ConversationID == "" {
		s.fail("The server kept no conversation for this answer, so it cannot be rated.")
		return
	}
	key := s.client.APIKey
	if t.Key != s.keyName || t.Server != s.baseURL {
		k, ok := s.cfg.Keys[t.Key]
		if !ok {
			s.fail("This answer came with the key " + t.Key + ", which is not stored any more.")
			return
		}
		key = k
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	s.scr.Busy("Sending feedback…", cancel)
	err := sendFeedback(ctx, t.Server, key, t.ConversationID, t.Index, feedback)
	s.scr.Busy("", nil)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.note("Not sent.")
			return
		}
		s.fail("Could not send the feedback: " + err.Error())
		return
	}
	msg := "Feedback sent: a good answer"
	if feedback == "dislike" {
		msg = "Feedback sent: a bad answer"
	}
	if comment != "" {
		msg += " (the server keeps no comment)"
	}
	s.scr.Add(display.Done(msg))
}

// sendFeedback posts feedback ("like", "dislike") on answer index of a
// server conversation, authenticated by the agent's key it was held with.
func sendFeedback(ctx context.Context, baseURL, apiKey, conversationID string, index int, feedback string) error {
	body, _ := json.Marshal(map[string]any{
		"feedback": feedback, "question_index": index, "conversation_id": conversationID, "api_key": apiKey,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/feedback", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out)
	switch {
	case resp.StatusCode == http.StatusOK && out.Success:
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return errors.New("the server did not accept the key")
	case resp.StatusCode == http.StatusNotFound && out.Message != "":
		return errors.New("the server no longer has this conversation for the key")
	case out.Message != "":
		return errors.New(display.StripControls(out.Message))
	}
	return fmt.Errorf("the server answered %s", resp.Status)
}
