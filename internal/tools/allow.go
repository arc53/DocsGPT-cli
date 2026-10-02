package tools

import (
	"regexp"
	"runtime"
	"strings"
	"unicode"
)

// alwaysKey returns what "Always allow" covers for command, or "" when it
// must not be offered: the program and its first argument when that is a
// subcommand word ("git status", "npm test"), else the program alone
// ("ls"). Every command is checked, so a later one runs unasked only when
// it has the same key and passes the same checks:
//   - a simple command: no shell operators, substitutions, globs, escapes,
//     comments or several lines (quotes are fine, except on Windows);
//   - no leading VAR=value, and the program not given as a path;
//   - not a program that runs other programs or code (runsCode);
//   - no argument that makes a program run code or change what it works
//     on (riskyArg), such as git -c, -C or --upload-pack;
//   - not a subcommand that does (git config, npm exec).
func alwaysKey(command string) string {
	words := simpleWords(command)
	if len(words) == 0 {
		return ""
	}
	prog := words[0]
	name := strings.TrimSuffix(strings.ToLower(prog), ".exe")
	if prog == "" || strings.ContainsAny(prog, `/\=`) || runsCode(name) {
		return ""
	}
	for _, w := range words[1:] {
		if riskyArg(w) {
			return ""
		}
	}
	if len(words) == 1 || !subcommandRe.MatchString(words[1]) {
		return prog
	}
	if riskySubcommands[name+" "+words[1]] {
		return ""
	}
	return prog + " " + words[1]
}

var subcommandRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// simpleWords splits a command into words the way a shell would when it
// needs nothing but quotes for that, else returns nil.
func simpleWords(command string) []string {
	var (
		words []string
		word  strings.Builder
		in    bool // inside a word
		quote rune // the open quote, if any
	)
	special := "\\`$;&|<>(){}[]*?#!"
	if runtime.GOOS == "windows" { // cmd.exe: no ' quoting, % and ^ expand
		special += `'"%^`
	}
	for _, r := range command {
		switch {
		case r == '\t' && quote == 0, r == ' ' && quote == 0:
			if in {
				words, in = append(words, word.String()), false
				word.Reset()
			}
			continue
		case !unicode.IsPrint(r) && r != ' ' && r != '\t',
			strings.ContainsRune("\\`$", r), // expand even inside double quotes
			quote == 0 && strings.ContainsRune(special, r):
			return nil
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
		case r == quote:
			quote = 0
		default:
			word.WriteRune(r)
		}
		in = true
	}
	if quote != 0 {
		return nil
	}
	if in {
		words = append(words, word.String())
	}
	return words
}

// codeRunners run other programs or code they are handed: shells, wrappers,
// interpreters, remote shells, editors and pagers with shell escapes, build
// and archive tools that run commands. Allowing one would allow anything.
var codeRunners = map[string]bool{}

func init() {
	for _, n := range strings.Fields(`
		sh bash zsh fish dash ksh mksh csh tcsh ash busybox nu elvish xonsh
		cmd powershell pwsh start wsl runas wscript cscript mshta rundll32
		env sudo doas su nohup time timeout nice ionice chroot setsid stdbuf unbuffer
		xargs parallel eval exec command builtin source . watch script expect flock nsenter
		strace ltrace gdb lldb taskset chrt caffeinate systemd-run open xdg-open
		ssh scp sftp rsync telnet nc ncat socat
		find fd fdfind awk gawk mawk nawk sed osascript tclsh wish irb jshell julia r rscript
		deno bun npx pnpx bunx uvx
		vi vim nvim view ex emacs nano less more man
		make gmake bmake cmake tar gtar bsdtar zip 7z`) {
		codeRunners[n] = true
	}
}

// runsCode reports whether the program (lower case, without .exe) runs
// arbitrary code, versioned interpreters (python3.12, perl5) included.
func runsCode(name string) bool {
	for _, p := range []string{"python", "pypy", "perl", "ruby", "php", "lua", "node"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return codeRunners[name]
}

// riskySubcommands run commands of their own or change what later
// commands run (git config core.fsmonitor, git submodule foreach).
var riskySubcommands = map[string]bool{
	"git config": true, "git submodule": true, "git bisect": true, "git filter-branch": true,
	"git difftool": true, "git mergetool": true,
	"npm exec": true, "npm x": true, "npm explore": true, "pnpm exec": true, "pnpm dlx": true,
	"yarn exec": true, "yarn dlx": true, "bun x": true,
}

// riskyOptions are long options (by name, any spelling after it) that make
// a program run a command, write where it is told or work on another
// repository.
var riskyOptions = []string{
	"exec", "eval", "checkpoint-action", "upload-pack", "receive-pack", "git-dir", "work-tree",
	"config", "template", "to-command", "use-compress-program", "rsh", "command", "shell",
	"output", "editor", "pager",
}

// riskyArg reports whether an argument is a risky option: -c, -C, -e, -o,
// -x or -X alone, with a value attached (-ofile) or in a short cluster
// (-ec), a riskyOptions name after one or two dashes (--exec-path=…,
// -exec), or rg's --pre.
func riskyArg(w string) bool {
	body, ok := strings.CutPrefix(w, "-")
	if !ok || body == "" {
		return false
	}
	long := strings.HasPrefix(body, "-")
	if !long && (strings.ContainsRune("cCeoxX", rune(body[0])) ||
		len(body) <= 3 && strings.ContainsAny(body, "cCeoxX")) {
		return true
	}
	body, _, _ = strings.Cut(strings.TrimPrefix(body, "-"), "=")
	if body == "pre" {
		return true
	}
	for _, o := range riskyOptions {
		if strings.HasPrefix(body, o) {
			return true
		}
	}
	return false
}
