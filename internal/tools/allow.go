package tools

import (
	"regexp"
	"runtime"
	"slices"
	"strings"
	"unicode"
)

// alwaysKey returns what "Always allow" covers for command, or "" when it
// must not be offered: the program and its subcommand ("git status", "npm
// test"), or the program alone when it has no subcommands ("ls", "grep")
// or no other words than options ("git --version"). Every command is
// checked, so a later one runs unasked only when it has the same key and
// passes the same checks:
//   - a simple command: no shell operators, substitutions, globs, escapes,
//     comments or several lines (quotes are fine, except on Windows);
//   - no leading VAR=value, and the program not given as a path;
//   - not a program that runs other programs or code (runsCode);
//   - no argument that makes a program run code or change what it works
//     on (riskyArg), such as git -c, -C or --upload-pack;
//   - the subcommand is the first word: an option before it may take the
//     next word as its value (git --namespace status push runs git push),
//     so only options known to take none may come first (git --no-pager);
//   - not a subcommand that runs code (git config, npm exec).
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
	rest := words[1:]
	for len(rest) > 0 && bareOptions[name+" "+rest[0]] {
		rest = rest[1:]
	}
	switch {
	case plainPrograms[name] || !slices.ContainsFunc(rest, func(w string) bool { return !strings.HasPrefix(w, "-") }):
		return prog
	case !subcommandRe.MatchString(rest[0]) || riskySubcommands[name+" "+rest[0]]:
		return ""
	}
	return prog + " " + rest[0]
}

// plainPrograms have no subcommands and only read, so their key is the
// program whatever its arguments.
var plainPrograms = setOf(`ls cat head tail wc grep egrep fgrep rg ag ack tree du df stat file
	diff cmp nl cut column jq pwd echo printf which whoami uname id date ps
	realpath readlink basename dirname md5sum sha1sum sha256sum shasum cksum od hexdump strings`)

// bareOptions take no value and change nothing the key is about, so they
// may come before the subcommand.
var bareOptions = setOf(`git|--no-pager git|-P git|--no-optional-locks
	npm|-s npm|--silent npm|-q npm|--quiet`)

func setOf(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[strings.ReplaceAll(w, "|", " ")] = true
	}
	return m
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
// interpreters, database shells, remote shells, editors and pagers with
// shell escapes, build, task and archive tools that run commands. Allowing
// one would allow anything.
var codeRunners = setOf(`
	sh bash zsh fish dash ksh mksh csh tcsh ash busybox nu elvish xonsh
	cmd powershell pwsh start wsl runas wscript cscript mshta rundll32
	env sudo doas su nohup time timeout nice ionice chroot setsid stdbuf unbuffer
	xargs parallel eval exec command builtin source . watch script expect flock nsenter
	strace ltrace gdb lldb taskset chrt caffeinate systemd-run open xdg-open
	tmux screen at batch crontab direnv entr nix nix-shell
	ssh scp sftp rsync telnet nc ncat socat
	find fd fdfind awk gawk mawk nawk sed osascript tclsh wish irb jshell julia r rscript
	deno bun npx pnpx bunx uvx pipx tsx ts-node
	sqlite3 duckdb psql mysql mariadb mongo mongosh
	vi vim nvim view ex emacs nano less more man
	make gmake bmake cmake just task rake mvn gradle ant sbt dotnet ansible ansible-playbook
	tar gtar bsdtar zip 7z`)

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

// riskySubcommands run code named in their arguments (go run x@latest,
// npm install pkg, docker run, kubectl exec), change what later commands
// run (git config core.fsmonitor, go env -w GOFLAGS=...) or hand out
// credentials (git credential fill). Running a project's own scripts (npm
// test, cargo run) is what allowing them means.
var riskySubcommands = setOf(`
	git|config git|submodule git|bisect git|filter-branch git|difftool git|mergetool
	git|credential git|send-email
	go|run go|generate go|env go|install cargo|install
	npm|exec npm|x npm|explore npm|install npm|i npm|in npm|add npm|isntall npm|install-test npm|it
	npm|link npm|ln npm|init npm|create npm|config npm|set
	pnpm|exec pnpm|dlx pnpm|add pnpm|install pnpm|i pnpm|create pnpm|config
	yarn|exec yarn|dlx yarn|add yarn|create yarn|node yarn|config
	pip|install pip|download pip|wheel pip3|install pip3|download pip3|wheel
	uv|run uv|pip uv|tool uv|add poetry|run poetry|add pipenv|run pipenv|install conda|run
	gem|install bundle|exec brew|install brew|reinstall
	docker|run docker|exec docker|create docker|compose docker|container docker|build docker|buildx
	podman|run podman|exec podman|create podman|compose podman|container podman|build
	kubectl|exec kubectl|run kubectl|debug kubectl|attach kubectl|cp kubectl|plugin
	gh|alias gh|extension gh|ext`)

// riskyOptions are long options (by name, any spelling after it) that make
// a program run a command, write where it is told or work on another
// repository or project.
var riskyOptions = []string{
	"exec", "eval", "checkpoint-action", "upload-pack", "receive-pack", "git-dir", "work-tree",
	"config", "template", "to-command", "use-compress-program", "compress-program", "rsh",
	"command", "shell", "script-shell", "node-options", "output", "editor", "pager",
	"open-files-in-pager", "toolexec", "vettool", "ldflags", "extld", "manifest-path", "prefix",
	"kubeconfig", "userconfig", "globalconfig", "hostname-bin", "diff-program",
}

// riskyArg reports whether an argument is a risky option: -c, -C, -e, -o,
// -O, -x or -X alone, with a value attached (-ofile) or in a short cluster
// (-ec), a riskyOptions name after one or two dashes (--exec-path=…,
// -exec), or rg's --pre.
func riskyArg(w string) bool {
	body, ok := strings.CutPrefix(w, "-")
	if !ok || body == "" {
		return false
	}
	long := strings.HasPrefix(body, "-")
	if !long && (strings.ContainsRune("cCeoOxX", rune(body[0])) ||
		len(body) <= 3 && strings.ContainsAny(body, "cCeoOxX")) {
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
