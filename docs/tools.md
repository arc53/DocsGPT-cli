# Tools and approval

In chats and one-shot answers, the agent can work on your machine through three
tools:

| Tool | What it does | Asks you |
|---|---|---|
| `run_command` | Runs a command with `sh -c` (`cmd /C` on Windows) | Always |
| `read_file` | Reads a text file, in pages of up to 2000 lines or 50 KB | Only outside the working directory, or for files that may hold secrets |
| `write_file` | Writes a file, creating missing directories | Always, with a short diff first |

Each call shows as a block: the command or path, the output's last lines while
it runs, then `✓ exit 0 · 1.2s` or `✗ exit 1 · 0.3s`. In a terminal with
colors the block sits on a subtle background: gray while it runs, tinted green
once it succeeded, red when it failed or was denied or cancelled. The model
gets the output's last 2000 lines or 50 KB, and the exit code.

In the chat a block shows the output's last 5 lines under
`… 195 earlier lines · ctrl+o to expand`. Ctrl+O expands every tool block to
the whole output the model got, and collapses them again; it holds for the
blocks that follow too. A one-shot question shows the last lines only.

## Approving

```
$ git log --oneline -5
→ Approve    Always allow git log    Always approve    Deny    Edit
run once
←→ choose · enter confirm · esc cancel
```

| Choice | Key | Effect |
|---|---|---|
| Approve | `a`, `y` | Run it once |
| Always allow | `l` | Run it, and similar calls for the rest of the session, without asking |
| Always approve | `p` | Run it, and every later tool call of the session, without asking |
| Deny | `d`, `n` | Don't run it; the model is told you declined |
| Edit | `e` | Change the command, then decide again (the model is told what ran) |

Always allow is offered only when there is a safe narrow scope (see below);
the line under the choices says what the highlighted one does. Ctrl+C or Esc at
the prompt stops the whole answer. In the chat the choices open above the
input; what you were typing as they appeared stays in the input instead of
answering them.

Commands run without your terminal: one that asks for a password fails instead
of waiting. They get `--tool-timeout` seconds (default 30) and are killed with
their child processes on timeout or Ctrl+C. On Windows they run through
`cmd /C` with `NoDefaultCurrentDirectoryInExePath` set, so a program name never
resolves to a file in the working directory (`git` is never `.\git.exe`).

## Always allow

"Always allow" lasts until the chat (or the one-shot run) ends.

- **Reads and writes:** covers every later read (or write).
- **Commands:** covers later commands with the same key, shown on the choice:
  the program and its subcommand, such as `git status` or `npm test`. Programs
  that only read and have no subcommands (`ls`, `cat`, `grep`, `rg`, `head`,
  `wc`, `jq`, …) key on the program alone, whatever their arguments, and so
  does a program given nothing but options (`git --version`).

Every later command is checked again, and runs unasked only when it has the
same key and passes the same checks. These always ask, and are never offered
"Always allow":

- anything with `;`, `&`, `|`, redirections, `$(…)`, backticks, globs, escapes,
  comments or several lines (quotes are fine, except on Windows);
- a leading `VAR=value`, or the program given as a path (`./run`, `/bin/ls`);
- programs that run other programs or code: shells, `sudo`, `env`, `xargs`,
  interpreters (`python`, `node`, …), `ssh`, `find`, `awk`, `sed`, editors,
  pagers, `make`, `tar` and the like;
- options that make a program run code or work elsewhere, such as `git -c`,
  `git -C`, `--exec`, `--upload-pack`, `--output`;
- subcommands that do, such as `git config`, `git submodule`, `npm exec`,
  `npm install`, `docker run`;
- a path argument that may leave the working directory: absolute, from `~`, or
  through `..` (`cat ../.env`, `ls ~/.ssh`, `git diff --output=/tmp/x`);
- options before the subcommand, except a few known to take no value
  (`git --no-pager`, `git -P`, `npm -s`): `git --namespace status push` would
  run `git push`.

**Known gap:** the key is the subcommand, not what its options do. Allowing
`git branch` also allows `git branch -D main`, and allowing `git stash` allows
`git stash drop`. Allow a subcommand only when you are fine with all of its
forms for the rest of the session.

## Always approve

"Always approve" runs every later call without asking: commands of any kind,
writes and reads, until the chat (or the one-shot run) ends. It is
`--auto-approve` switched on mid-session. In a chat the footer then shows
`auto-approve`, and `/approve` switches it off (or on). Earlier "Always allow"
choices still hold after it is switched off. Each call still shows its block.

## Reads

Reading a file inside the working directory runs right away. These ask first:

- files outside the working directory (a symlink's target counts, and the
  prompt shows where it leads);
- any file when the working directory is your home directory or above it
  (compared as directories, so `/Users/Me` and `/users/me` are the same);
- paths that look like they hold secrets, anywhere on the way: `.env*`,
  `.envrc`, `.ssh`, `.aws`, `.azure`, `.kube`, `.docker`, `.gnupg`, `gcloud`,
  `.docsgpt`, `.netrc`, `.npmrc`, `.yarnrc.yml`, `.pypirc`, `.pgpass`,
  `.my.cnf`, `.s3cfg`, `.terraformrc`, `.vault-token`, `.git-credentials`,
  `auth.json`, `secrets`/`secrets.*`, `credentials*`, `client_secret*`, `id_*`
  keys, `*.pem`, `*.key`, `*.p12`, `*.ppk`, `*.kdbx`, keychains, `*.ovpn`,
  `*.tfstate`, `*.tfvars`, shell histories and similar.

Devices, pipes and directories are refused, and binary files are not sent.

## Turning tools off or on

| Flag | Effect |
|---|---|
| `--no-tools` | Don't offer the tools at all |
| `--auto-approve` | Run every call without asking |
| `--tool-timeout <s>` | Time limit for each command |

In a one-shot question, the tools are offered only when someone can approve
them: stdin is a terminal, or you pass `--auto-approve`. `--no-context` does
not turn them off.

## Security

Treat what the model asks to run as untrusted. A command runs with your user's
permissions, and the model can be steered by what it reads: a web page, a
source document, a file in the repository, a command's output (prompt
injection).

**Approval is the only safeguard.** There is no sandbox and no blocklist. Read
what you approve, and prefer Edit or Deny when a command does more than you
asked for. The prompt shows commands, paths and source titles with control
characters made visible (`␛`, `␍`, `␊`), so an escape sequence cannot hide part
of what you approve.

- `--auto-approve`, "Always approve" and `/approve` remove the safeguard. Use
  them only where a wrong command cannot hurt: a container, a VM, a throwaway
  checkout.
- "Always allow" is narrow on purpose; it is still trust in the model for the
  rest of the session.
- Chats are saved in `~/.docsgpt/sessions/`, with tool arguments and output.
  Review a session before you share an `/export` of it.
- Use version control or backups before letting the agent write files.

[Host mode](host.md) is different: there the agent runs commands with no one at
the machine, and approval is set in the DocsGPT web app.
