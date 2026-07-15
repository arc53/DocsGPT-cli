package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvFileName is the dotenv file consulted for ${VAR} interpolation. The suite
// directory's .env wins over the working directory's; real environment
// variables win over both — so a committed suite can reference ${KEYS} that
// each machine provides via its shell, a local .env, or CI secrets.
const EnvFileName = ".env"

// envLookup returns the interpolation source for a suite rooted at dir:
// os.LookupEnv first, then dir/.env, then ./.env.
func envLookup(dir string) func(string) (string, bool) {
	merged := map[string]string{}
	if cwd, err := os.Getwd(); err == nil && cwd != dir {
		for k, v := range parseDotEnvFile(filepath.Join(cwd, EnvFileName)) {
			merged[k] = v
		}
	}
	for k, v := range parseDotEnvFile(filepath.Join(dir, EnvFileName)) {
		merged[k] = v
	}
	return func(name string) (string, bool) {
		if v, ok := os.LookupEnv(name); ok {
			return v, true
		}
		v, ok := merged[name]
		return v, ok
	}
}

// parseDotEnvFile reads a dotenv file: KEY=VALUE lines, `#` comments, an
// optional `export ` prefix, and single- or double-quoted values. A missing or
// unreadable file yields nil. Values are never written into the process
// environment — they only feed ${VAR} interpolation.
func parseDotEnvFile(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	vars := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		vars[key] = val
	}
	return vars
}

// expandEnv replaces ${VAR} references in a YAML document using lookup. Only
// the braced form is expanded ($HOME passes through untouched, so prose
// containing dollar signs is safe); $$ escapes a literal dollar. An unset
// variable or a malformed reference is a hard error — silently benchmarking
// with an empty API key or a token-less webhook URL is never acceptable.
// Full-line comments are left untouched so templates can show ${VAR} examples.
func expandEnv(doc string, lookup func(string) (string, bool)) (string, error) {
	if !strings.Contains(doc, "$") {
		return doc, nil
	}
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue // comment line: never expanded
		}
		expanded, err := expandLine(line, lookup)
		if err != nil {
			return "", fmt.Errorf("line %d: %w", i+1, err)
		}
		lines[i] = expanded
	}
	return strings.Join(lines, "\n"), nil
}

func expandLine(s string, lookup func(string) (string, bool)) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		switch {
		case i+1 < len(s) && s[i+1] == '$': // $$ -> literal $
			b.WriteByte('$')
			i += 2
		case i+1 < len(s) && s[i+1] == '{':
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", fmt.Errorf("unterminated ${ reference (use $$ for a literal dollar)")
			}
			name := s[i+2 : i+2+end]
			if name == "" {
				return "", fmt.Errorf("empty ${} reference")
			}
			v, ok := lookup(name)
			if !ok {
				return "", fmt.Errorf("environment variable %s is not set (set it, or add it to %s)", name, EnvFileName)
			}
			b.WriteString(v)
			i += 2 + end + 1
		default: // bare $ passes through
			b.WriteByte('$')
			i++
		}
	}
	return b.String(), nil
}
