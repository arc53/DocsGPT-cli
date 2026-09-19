package manage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// KindAgent is the only document kind the import API accepts.
const KindAgent = "Agent"

// Document is one YAML document destined for the agent import API.
type Document struct {
	Source string // "agents/support.yaml", "agents/all.yaml#2", or "<stdin>"
	Index  int    // 1-based position within its file
	Text   string // the document text exactly as written (comments included)

	Kind       string
	APIVersion string
	Name       string // spec.name
	ID         string // metadata.id
	Slug       string // metadata.slug
}

// Label names the document in plans and errors.
func (d Document) Label() string {
	if d.Name != "" {
		return fmt.Sprintf("%s (%s)", d.Source, d.Name)
	}
	return d.Source
}

// docHeader is the part of a document the CLI inspects locally.
type docHeader struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		ID   string `yaml:"id"`
		Slug string `yaml:"slug"`
	} `yaml:"metadata"`
	Spec struct {
		Name string `yaml:"name"`
	} `yaml:"spec"`
}

// LoadDocuments expands -f arguments into agent documents, in order. Each
// argument is a file, a directory (its *.yaml / *.yml files, sorted by name,
// not recursive) or "-" for stdin. Multi-document files are split. A document
// whose kind is not Agent, or that is not valid YAML, is an error; nothing is
// returned in that case so a bad file never leads to a partial apply.
func LoadDocuments(args []string, stdin io.Reader) ([]Document, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no input: pass -f <file|dir|->")
	}
	stdinArgs := 0
	for _, arg := range args {
		if arg == "-" {
			stdinArgs++
		}
	}
	if stdinArgs > 1 {
		return nil, fmt.Errorf("-f - (stdin) can only be given once")
	}
	var docs []Document
	for _, arg := range args {
		if arg == "-" {
			if stdin == nil {
				return nil, fmt.Errorf("-f -: stdin is not available")
			}
			data, err := io.ReadAll(stdin)
			if err != nil {
				return nil, fmt.Errorf("read stdin: %w", err)
			}
			parsed, err := ParseDocuments("<stdin>", data)
			if err != nil {
				return nil, err
			}
			if len(parsed) == 0 {
				return nil, fmt.Errorf("<stdin>: no YAML documents found")
			}
			docs = append(docs, parsed...)
			continue
		}

		st, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		files := []string{arg}
		if st.IsDir() {
			files, err = yamlFilesIn(arg)
			if err != nil {
				return nil, err
			}
			if len(files) == 0 {
				return nil, fmt.Errorf("%s: no *.yaml or *.yml files found", arg)
			}
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			parsed, err := ParseDocuments(f, data)
			if err != nil {
				return nil, err
			}
			if len(parsed) == 0 && !st.IsDir() {
				return nil, fmt.Errorf("%s: no YAML documents found", f)
			}
			docs = append(docs, parsed...)
		}
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no agent documents found")
	}
	return docs, nil
}

// yamlFilesIn lists dir's *.yaml / *.yml files sorted by name.
func yamlFilesIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".yaml", ".yml":
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// ParseDocuments splits data into its YAML documents and validates each one
// locally. Empty documents (blank, or comments only) are dropped.
func ParseDocuments(source string, data []byte) ([]Document, error) {
	chunks := SplitYAMLDocuments(string(data))
	multi := len(chunks) > 1
	var docs []Document
	for i, text := range chunks {
		label := source
		if multi {
			label = fmt.Sprintf("%s#%d", source, i+1)
		}
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(text), &node); err != nil {
			return nil, fmt.Errorf("%s: invalid YAML: %w", label, err)
		}
		if node.Kind == 0 {
			continue // comments / whitespace only
		}
		if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s: top-level YAML must be a mapping", label)
		}
		var head docHeader
		if err := node.Decode(&head); err != nil {
			return nil, fmt.Errorf("%s: invalid agent document: %w", label, err)
		}
		if head.Kind != KindAgent {
			return nil, fmt.Errorf("%s: unsupported kind %q (only kind: %s can be applied)", label, head.Kind, KindAgent)
		}
		if !strings.HasPrefix(head.APIVersion, "docsgpt.") {
			return nil, fmt.Errorf("%s: unsupported apiVersion %q (expected docsgpt.arc53.com/v1)", label, head.APIVersion)
		}
		if head.Spec.Name == "" {
			return nil, fmt.Errorf("%s: spec.name is required", label)
		}
		docs = append(docs, Document{
			Source:     label,
			Index:      i + 1,
			Text:       text,
			Kind:       head.Kind,
			APIVersion: head.APIVersion,
			Name:       head.Spec.Name,
			ID:         head.Metadata.ID,
			Slug:       head.Metadata.Slug,
		})
	}
	return docs, nil
}

// SplitYAMLDocuments splits a YAML stream on its document markers, keeping
// each document's text verbatim (the server receives exactly what was
// written). A `---` line starts a document and a `...` line ends one; the YAML
// spec forbids both at column 0 inside any scalar, so a textual split is
// exact. Chunks that are entirely blank are dropped; chunks holding only
// comments are kept and discarded by ParseDocuments.
func SplitYAMLDocuments(text string) []string {
	text = strings.TrimPrefix(text, "\ufeff")
	var (
		docs []string
		cur  strings.Builder
	)
	flush := func() {
		if strings.TrimSpace(cur.String()) != "" {
			docs = append(docs, cur.String())
		}
		cur.Reset()
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		bare := strings.TrimRight(line, "\r\n")
		switch {
		case isMarker(bare, "---"):
			flush()
			// `--- <content>` may carry the first node on the marker line.
			if rest := strings.TrimSpace(bare[3:]); rest != "" && !strings.HasPrefix(rest, "#") {
				cur.WriteString(rest + "\n")
			}
		case isMarker(bare, "..."):
			flush()
		case strings.HasPrefix(bare, "%") && strings.TrimSpace(cur.String()) == "":
			// %YAML / %TAG directive before a document: not part of any doc.
		default:
			cur.WriteString(line)
		}
	}
	flush()
	return docs
}

// isMarker reports whether line is the marker alone or followed by whitespace.
func isMarker(line, marker string) bool {
	if !strings.HasPrefix(line, marker) {
		return false
	}
	rest := line[len(marker):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}
