// Package context describes the user's environment to the agent, as a
// <context> block put before the first message of a conversation (the
// server ignores system messages unless the agent allows prompt overrides).
package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/config"
)

const (
	maxEntries      = 50        // directory entries listed
	maxInstructions = 12 * 1024 // bytes of AGENTS.md / CLAUDE.md, all files together
)

// Build returns the context block for the working directory, or "" when
// the settings send nothing.
func Build(s config.Settings) string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	var b strings.Builder
	if s.SendCurrentDirectory && cwd != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", cwd)
	}
	if s.SendDirectoryContents && cwd != "" {
		if l := listing(cwd); l != "" {
			fmt.Fprintf(&b, "Files in it: %s\n", l)
		}
	}
	if s.SendLastCommands {
		if cmds := lastCommands(s.NumberOfLastCommands); len(cmds) > 0 {
			b.WriteString("Recent shell commands:\n")
			for _, c := range cmds {
				b.WriteString("  $ " + c + "\n")
			}
		}
	}
	if s.SendProjectInstructions && cwd != "" {
		for _, f := range instructions(cwd) {
			fmt.Fprintf(&b, "Project instructions from %s:\n<instructions>\n%s\n</instructions>\n", f.name, f.text)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "<context>\nThe user is asking from a terminal (docsgpt-cli). Their environment:\n" + b.String() + "</context>"
}

// Prepend puts the context block before text, when there is one.
func Prepend(block, text string) string {
	if block == "" {
		return text
	}
	return block + "\n\n" + text
}

// listing names the first maxEntries entries of dir, directories marked
// with a slash.
func listing(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || name == ".DS_Store" {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	if len(names) > maxEntries {
		return strings.Join(names[:maxEntries], ", ") + fmt.Sprintf(" (+%d more)", len(names)-maxEntries)
	}
	return strings.Join(names, ", ")
}

type instructionFile struct{ name, text string }

// instructions reads AGENTS.md (else CLAUDE.md) of every directory from the
// git root down to cwd (cwd alone outside a repository), outermost first,
// within maxInstructions bytes in all.
func instructions(cwd string) []instructionFile {
	dirs := []string{cwd}
	for d := cwd; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			dirs = dirs[:1] // not in a repository
			break
		}
		d = parent
		dirs = append(dirs, d)
	}
	var out []instructionFile
	budget := maxInstructions
	for i := len(dirs) - 1; i >= 0 && budget > 0; i-- {
		for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
			path := filepath.Join(dirs[i], name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			text := strings.TrimSpace(string(data))
			if len(text) > budget {
				text = strings.ToValidUTF8(text[:budget], "") + "\n[… truncated]"
			}
			budget -= len(text)
			if rel, err := filepath.Rel(cwd, path); err == nil {
				path = rel
			}
			out = append(out, instructionFile{path, text})
			break
		}
	}
	return out
}

// lastCommands returns the last n commands of the user's shell history
// (zsh, bash or fish), or nothing when it cannot be read.
func lastCommands(n int) []string {
	home, err := os.UserHomeDir()
	if err != nil || n <= 0 {
		return nil
	}
	shell := filepath.Base(os.Getenv("SHELL"))
	var file string
	switch shell {
	case "zsh":
		file = filepath.Join(home, ".zsh_history")
	case "bash":
		file = filepath.Join(home, ".bash_history")
	case "fish":
		file = filepath.Join(home, ".local", "share", "fish", "fish_history")
	default:
		return nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var cmds []string
	for _, line := range strings.Split(string(data), "\n") {
		if shell == "fish" {
			if c, ok := strings.CutPrefix(line, "- cmd: "); ok {
				cmds = append(cmds, c)
			}
			continue
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, ": ") { // zsh extended history ": <time>:<duration>;cmd"
			_, line, _ = strings.Cut(line, ";")
		}
		if line != "" {
			cmds = append(cmds, line)
		}
	}
	return cmds[max(0, len(cmds)-n):]
}
