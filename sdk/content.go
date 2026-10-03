package docsgpt

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

// ContentPart is one part of a multimodal message, as the OpenAI content
// array has it: text, an image (image_url) or a file (file). DocsGPT stores
// the images and files sent inline as the conversation's attachments.
type ContentPart struct {
	Type     string    `json:"type"` // "text", "image_url" or "file"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
	File     *FileData `json:"file,omitempty"`
}

// ImageURL is an image_url part's image: a URL, or the image itself as a
// data URL.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// FileData is a file part's file: its bytes as a data URL (FileData), or
// the id of a file the server already holds.
type FileData struct {
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
	FileID   string `json:"file_id,omitempty"`
}

// TextPart returns a text part.
func TextPart(text string) ContentPart { return ContentPart{Type: "text", Text: text} }

// ImagePart returns an image_url part carrying the image as a data URL.
func ImagePart(mimeType string, data []byte) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: dataURL(mimeType, data)}}
}

// FilePart returns a file part carrying the file as a data URL. The server
// reads it by its name's extension (a PDF, a spreadsheet, text).
func FilePart(filename, mimeType string, data []byte) ContentPart {
	return ContentPart{Type: "file", File: &FileData{Filename: filename, FileData: dataURL(mimeType, data)}}
}

// AttachmentPart returns the part for a file named filename: an image_url
// part for a PNG, JPEG, GIF or WebP image (told by its bytes), else a file
// part, its type taken from the name.
func AttachmentPart(filename string, data []byte) ContentPart {
	if t := ImageType(data); t != "" {
		return ImagePart(t, data)
	}
	return FilePart(filename, MimeType(filename), data)
}

// ImageType returns the media type of an image the models read (PNG, JPEG,
// GIF or WebP), told by its first bytes; "" for anything else.
func ImageType(data []byte) string {
	switch t := http.DetectContentType(data); t {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return t
	}
	return ""
}

// mimeTypes are the types of the files a DocsGPT server reads, which the
// system's table may not know (or know differently).
var mimeTypes = map[string]string{
	".md":   "text/markdown",
	".mdx":  "text/markdown",
	".rst":  "text/x-rst",
	".txt":  "text/plain",
	".csv":  "text/csv",
	".json": "application/json",
	".pdf":  "application/pdf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".epub": "application/epub+zip",
	".html": "text/html",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".zip":  "application/zip",
}

// MimeType returns the media type of a file named filename, without
// parameters: from its extension, application/octet-stream when unknown.
func MimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if t, ok := mimeTypes[ext]; ok {
		return t
	}
	t := mime.TypeByExtension(ext)
	if t == "" {
		return "application/octet-stream"
	}
	t, _, _ = strings.Cut(t, ";")
	return strings.TrimSpace(t)
}

func dataURL(mimeType string, data []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// Text returns the message's text: Content, then the text parts, a line
// apart.
func (m Message) Text() string {
	texts := []string{}
	if m.Content != "" {
		texts = append(texts, m.Content)
	}
	for _, p := range m.Parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// message is Message as it goes over the wire, content a string or an
// array of parts.
type message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// MarshalJSON writes content as a string, or, with Parts, as the parts
// array: Content first as a text part, then Parts.
func (m Message) MarshalJSON() ([]byte, error) {
	w := message{Role: m.Role, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID}
	var err error
	switch {
	case len(m.Parts) > 0:
		parts := m.Parts
		if m.Content != "" {
			parts = append([]ContentPart{TextPart(m.Content)}, parts...)
		}
		w.Content, err = json.Marshal(parts)
	case m.Content != "":
		w.Content, err = json.Marshal(m.Content)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads content as a string into Content, or as an array
// into Parts, all of them (text parts too: see Text).
func (m *Message) UnmarshalJSON(b []byte) error {
	var w message
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*m = Message{Role: w.Role, ToolCalls: w.ToolCalls, ToolCallID: w.ToolCallID}
	c := strings.TrimSpace(string(w.Content))
	switch {
	case c == "" || c == "null":
		return nil
	case strings.HasPrefix(c, "["):
		return json.Unmarshal(w.Content, &m.Parts)
	}
	return json.Unmarshal(w.Content, &m.Content)
}
