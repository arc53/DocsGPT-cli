package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxImage is the largest image a result passes on to the model.
const maxImage = 20 << 20

// Result turns a tool's result into the text the model gets and the images
// that go with it. Other media are named, not sent.
func Result(r *mcp.CallToolResult) (string, []docsgpt.ContentPart) {
	var texts []string
	var images []docsgpt.ContentPart
	for _, c := range r.Content {
		switch c := c.(type) {
		case *mcp.TextContent:
			texts = append(texts, c.Text)
		case *mcp.ImageContent:
			if len(c.Data) > maxImage {
				texts = append(texts, fmt.Sprintf("[An image (%s, %s) too large to pass on.]", c.MIMEType, size(len(c.Data))))
				continue
			}
			images = append(images, docsgpt.ImagePart(c.MIMEType, c.Data))
			texts = append(texts, fmt.Sprintf("[Image %d (%s, %s): attached to this result.]", len(images), c.MIMEType, size(len(c.Data))))
		case *mcp.AudioContent:
			texts = append(texts, fmt.Sprintf("[Audio (%s, %s), not passed on.]", c.MIMEType, size(len(c.Data))))
		case *mcp.ResourceLink:
			texts = append(texts, fmt.Sprintf("[Resource %s: %s]", firstOf(c.Title, c.Name), c.URI))
		case *mcp.EmbeddedResource:
			switch {
			case c.Resource == nil:
			case c.Resource.Text != "":
				texts = append(texts, c.Resource.Text)
			default:
				texts = append(texts, fmt.Sprintf("[Resource %s (%s, %s), not passed on.]", c.Resource.URI, c.Resource.MIMEType, size(len(c.Resource.Blob))))
			}
		}
	}
	// Structured output comes as text too, by the spec; alone, it is the result.
	if len(texts) == 0 && r.StructuredContent != nil {
		if b, err := json.MarshalIndent(r.StructuredContent, "", "  "); err == nil {
			texts = append(texts, string(b))
		}
	}
	return strings.Join(texts, "\n"), images
}

func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
