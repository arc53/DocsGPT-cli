package docsgpt

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

// pngHeader is the start of a PNG file: enough for ImageType.
var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func TestMessageMarshalsContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  Message
		want string
	}{
		{"text", Message{Role: "user", Content: "hi"}, `{"role":"user","content":"hi"}`},
		{"no content", Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "t1", Function: FunctionCall{Name: "run"}}}},
			`{"role":"assistant","tool_calls":[{"id":"t1","function":{"name":"run"}}]}`},
		{"tool result", Message{Role: "tool", Content: "ok", ToolCallID: "t1"}, `{"role":"tool","content":"ok","tool_call_id":"t1"}`},
		{"parts after the text", Message{Role: "user", Content: "what is this?", Parts: []ContentPart{
			ImagePart("image/png", []byte("img")),
			FilePart("spec.pdf", "application/pdf", []byte("pdf")),
		}}, `{"role":"user","content":[` +
			`{"type":"text","text":"what is this?"},` +
			`{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1n"}},` +
			`{"type":"file","file":{"filename":"spec.pdf","file_data":"data:application/pdf;base64,cGRm"}}]}`},
		{"parts only", Message{Role: "user", Parts: []ContentPart{TextPart("a")}}, `{"role":"user","content":[{"type":"text","text":"a"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.msg)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("got  %s\nwant %s", b, tc.want)
			}
		})
	}
}

func TestMessageUnmarshalsContent(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"role":"user","content":"hi","tool_call_id":"x"}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Content != "hi" || m.ToolCallID != "x" || m.Parts != nil {
		t.Errorf("string content: %+v", m)
	}
	in := Message{Role: "user", Parts: []ContentPart{TextPart("look"), ImagePart("image/png", []byte("x")), TextPart("here")}}
	b, _ := json.Marshal(in)
	m = Message{Content: "stale"}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, in) {
		t.Errorf("parts did not round-trip:\n got  %+v\n want %+v", m, in)
	}
	if got := m.Text(); got != "look\nhere" {
		t.Errorf("Text() = %q", got)
	}
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":null}`), &m); err != nil || m.Content != "" || m.Parts != nil {
		t.Errorf("null content: %+v, %v", m, err)
	}
}

// A request with parts is what the server reads: the content array of the
// last user message.
func TestChatRequestCarriesParts(t *testing.T) {
	req := ChatRequest{Messages: []Message{{Role: "user", Content: "q", Parts: []ContentPart{AttachmentPart("shot.png", pngHeader)}}}}
	b, _ := json.Marshal(req)
	var raw struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	parts := raw.Messages[0].Content
	if len(parts) != 2 || parts[0]["type"] != "text" || parts[1]["type"] != "image_url" {
		t.Fatalf("parts = %v", parts)
	}
	url := parts[1]["image_url"].(map[string]any)["url"].(string)
	if want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngHeader); url != want {
		t.Errorf("url = %q", url)
	}
}

func TestAttachmentPart(t *testing.T) {
	// An image goes as image_url whatever its name; anything else as a
	// file with the type of its extension.
	if p := AttachmentPart("clipboard", pngHeader); p.Type != "image_url" {
		t.Errorf("image: %+v", p)
	}
	p := AttachmentPart("notes.md", []byte("# Notes"))
	if p.Type != "file" || p.File.Filename != "notes.md" || p.File.FileData != "data:text/markdown;base64,IyBOb3Rlcw==" {
		t.Errorf("markdown: %+v", p.File)
	}
	for name, want := range map[string]string{
		"a.PDF": "application/pdf", "a.json": "application/json", "a.xyz-unknown": "application/octet-stream", "a.csv": "text/csv",
	} {
		if got := MimeType(name); got != want {
			t.Errorf("MimeType(%q) = %q, want %q", name, got, want)
		}
	}
}
