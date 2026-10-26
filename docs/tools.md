# Tools and approval

In chats and one-shot answers, the agent can work on your machine through four
tools:

| Tool | What it does | Asks you |
|---|---|---|
| `run_command` | Runs a command with `sh -c` (`cmd /C` on Windows) | Always |
| `read_file` | Reads a text file, in pages of up to 2000 lines or 50 KB, or shows the model an image | Only outside the working directory, or for files that may hold secrets |
| `edit_file` | Replaces exact pieces of text in a file | Always, with the diff first |
| `write_file` | Writes a whole file, creating missing directories | Always, with a short diff first |

Paths may start with `~` (your home directory). [MCP servers](mcp.md) add
their own tools, approved the same way. Commands can also run in the
[background](#background-processes-and-monitors).

Each call shows as a block: the command or path, the output's last lines while
it runs, with `running · 12s` under them once it has taken a second, then
`✓ exit 0 · 1.2s` or `✗ exit 1 · 0.3s`. In a terminal with
colors the block sits on a subtle background: gray while it runs, tinted green
once it succeeded, red when it failed or was denied or cancelled. The model
gets the output's last 2000 lines or 50 KB, and the exit code.

In the chat a block shows the output's last 5 lines under
`… 195 earlier lines · ctrl+o to expand`. Ctrl+O expands every tool block to
the whole output the model got, and collapses them again; it holds for the
blocks that follow too. With the mouse on, a click on a block expands or
collapses that one alone. A one-shot question shows the last lines only.

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
of waiting. They get `--tool-timeout` seconds (default 30), or the timeout the
model asks for (at most an hour, or `--tool-timeout` when that is longer;
the title then shows it, `· timeout 10m`), and are killed with their child
processes on timeout or Ctrl+C. On Windows they run through
`cmd /C` with `NoDefaultCurrentDirectoryInExePath` set, so a program name never
resolves to a file in the working directory (`git` is never `.\git.exe`).

## Always allow

"Always allow" lasts until the chat (or the one-shot run) ends.

- **Reads:** covers every later read.
- **Writes:** covers later writes and edits of files in the working
  directory. Those that would ask as a read would (outside it, or a path that
  may hold secrets) and those inside a `.git` directory, whose hooks run code,
  still ask, and are not offered "Always allow writes".
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
- a command the model runs in another directory (`working_directory`) outside
  the working directory, or one that may hold secrets: "Always allow cat"
  covers `cat` here, not in `~/.ssh`;
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

PNG, JPEG, GIF, WebP and BMP images (told by their bytes, up to 20 MB) go to
the model as images: the block shows `✓ PNG · 1280×800 · 240 KB`. The server
scales them down for the model, and tells a model that reads no images what it
missed. This needs a DocsGPT server with images in tool results (October
2026); an older one gets the image as text, cut to its limit. In a chat, the
image goes with that answer only: the history and the saved session keep the
note naming the file, not its bytes.

## Edits

`edit_file` takes a path and a list of `old_text` → `new_text` replacements.
Each `old_text` must match one place in the file as it is, and no two may
overlap; all of them are matched before any applies. When the exact text is
not there, it matches again ignoring what models often get wrong: spaces at
the end of lines, `\r\n`, typographic quotes, dashes and spaces. The file keeps
its byte order mark, its `\r\n` line breaks (the new text gets them too) and
its permissions. You approve the change as a diff, and if the file changes
while the prompt waits, nothing is written and the model is told to read it
again. Files over 10 MB and binary files are refused.

## Background processes and monitors

A dev server, a watcher or a long build can keep running while the agent works
on, or after its answer:

| Tool | What it does | Asks you |
|---|---|---|
| `run_command` with `run_in_background` | Starts the command and returns at once with an id (`p1`); no time limit unless the model gives one | Like any command |
| `local_monitor` | Starts a command whose every output line is an event for the agent (`tail -f dev.log \| grep --line-buffered ERROR`); stops after 5 minutes, or the time the model gives (at most 30) | Like any command |
| `local_output` | Shows a process's status and last lines (100 by default), or lists them all | Never |
| `local_stop` | Stops a process with everything it started | Never |

The agent is told when a process exits (with its last 20 lines) and of each
line a monitor prints, as a `<local_process_event>`:

- while it works, with the result of its next tool call;
- in a chat with nothing running, in a turn of its own: the event shows as a
  `◆` block, and the agent answers it. After 20 such turns in a row without a
  message from you, events wait for your next message.

A monitor's lines within 200 ms make one event, at most 50 lines (the rest are
counted); after 10 quick events it gets one every 2 seconds, and one that
prints over 300 lines in 30 seconds is stopped, the agent told why.

These run on your machine, in this chat. They are not the DocsGPT server's
background jobs or monitors (`check_job`, `monitor_create`), and their
descriptions say so. Each runs in a process group of its own, and every one
still running is stopped when the chat ends (`Stopped 2 background
processes.`). In a one-shot question they are stopped when the answer ends,
and the agent hears of them only with its tool results.

In the chat:

- the footer counts what runs (`2 processes`);
- `/ps` lists them: pick one to show its last output lines or stop it (the
  agent is told with your next message);
- **Ctrl+B** while the agent's command runs moves it to the background: the
  agent is told at once and the command goes on as a background process.

Output goes to `~/.docsgpt/logs/processes/<chat>/p1.log` (readable only by
you), kept 3 days.

`run_command` refuses, without asking you, what would leave a process behind
out of reach: a trailing `&` (`a & b & wait` is fine), `nohup`, `setsid`,
`disown`, and on Windows `start` and `Start-Process`; and a bare `sleep` over
10 seconds (`timeout /t`, `Start-Sleep` on Windows) at the start of a command,
which would only block. The agent is told to use `run_in_background` (a loop
that ends when something is ready, `until curl -sf localhost:3000; do sleep 1;
done`) or `local_monitor` instead.

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
