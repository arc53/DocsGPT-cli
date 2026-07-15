package spec

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	SuiteFileName  = "bench.yaml"
	CaseFileName   = "case.yaml"
	GoldenFileName = "golden.json"
)

// Load reads a suite root: optional bench.yaml plus every directory under it
// (recursively) that contains a case.yaml. Hidden directories are skipped.
func Load(dir string) (*Suite, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("bench directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	suite := &Suite{Dir: abs, Name: filepath.Base(abs)}
	lookup := envLookup(abs)

	suitePath := filepath.Join(abs, SuiteFileName)
	if data, err := os.ReadFile(suitePath); err == nil {
		expanded, err := expandEnv(string(data), lookup)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", suitePath, err)
		}
		if err := strictUnmarshal([]byte(expanded), &suite.Config); err != nil {
			return nil, fmt.Errorf("%s: %w", suitePath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if suite.Config.Timeout < 0 || suite.Config.PollInterval < 0 {
		return nil, fmt.Errorf("%s: timeout and poll_interval must be positive", suitePath)
	}

	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != CaseFileName {
			return nil
		}
		c, err := loadCase(abs, filepath.Dir(path), lookup)
		if err != nil {
			return err
		}
		suite.Cases = append(suite.Cases, c)
		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(suite.Cases) == 0 {
		return nil, fmt.Errorf("no cases found under %s (looked for %s files)", abs, CaseFileName)
	}
	sort.Slice(suite.Cases, func(i, j int) bool { return suite.Cases[i].Name < suite.Cases[j].Name })

	for _, c := range suite.Cases {
		if err := suite.Validate(c); err != nil {
			return nil, err
		}
		for _, a := range c.Attachments {
			p := filepath.Join(c.Dir, filepath.Clean(a))
			if _, err := os.Stat(p); err != nil {
				return nil, fmt.Errorf("case %s: attachment %s: %w", c.Name, a, err)
			}
		}
	}
	return suite, nil
}

func loadCase(root, dir string, lookup func(string) (string, bool)) (*Case, error) {
	path := filepath.Join(dir, CaseFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded, err := expandEnv(string(data), lookup)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var c Case
	if err := strictUnmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return nil, err
	}
	c.Name = filepath.ToSlash(rel)
	if rel == "." {
		c.Name = filepath.Base(dir)
	}
	c.Dir = dir
	return &c, nil
}

// strictUnmarshal rejects unknown YAML fields so typos in case files fail
// loudly instead of silently skipping assertions.
func strictUnmarshal(data []byte, out any) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

// Filter returns cases whose name or description contains match
// (case-insensitive) and that carry at least one of tags (when given).
func (s *Suite) Filter(match string, tags []string) []*Case {
	var out []*Case
	match = strings.ToLower(match)
	for _, c := range s.Cases {
		if match != "" &&
			!strings.Contains(strings.ToLower(c.Name), match) &&
			!strings.Contains(strings.ToLower(c.Description), match) {
			continue
		}
		if len(tags) > 0 && !hasAnyTag(c.Tags, tags) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func hasAnyTag(have StringList, want []string) bool {
	for _, w := range want {
		for _, h := range have {
			if strings.EqualFold(h, w) {
				return true
			}
		}
	}
	return false
}

// Golden is the recorded output of a case (`bench record`), stored as
// golden.json in the case directory.
type Golden struct {
	Answer string `json:"answer"`
}

// GoldenPath returns the case's golden file location.
func (c *Case) GoldenPath() string { return filepath.Join(c.Dir, GoldenFileName) }

// LoadGolden reads the case's golden file; returns (nil, nil) if absent.
func (c *Case) LoadGolden() (*Golden, error) {
	data, err := os.ReadFile(c.GoldenPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var g Golden
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("%s: %w", c.GoldenPath(), err)
	}
	return &g, nil
}

// SaveGolden writes the case's golden file.
func (c *Case) SaveGolden(g *Golden) error {
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.GoldenPath(), append(data, '\n'), 0o644)
}
