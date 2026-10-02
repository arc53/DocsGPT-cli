# Chat

```bash
docsgpt-cli                      # open a chat
docsgpt-cli chat "first message" # open one with a message
docsgpt-cli -c                   # continue the latest chat in this directory
docsgpt-cli -r                   # pick an earlier chat to resume
```

`docsgpt-cli` with no arguments opens a chat when stdin and stdout are a
terminal. Answers stream in as markdown, followed by a short **Sources** list
(linked when the source has a URL).

The chat takes over the window: what it showed moves up into the scrollback
(nothing is erased), a short header opens it (version, keys, and the
`AGENTS.md` / `CLAUDE.md` files the context sends), and the input waits at the
bottom of the window, blank space above it until the conversation fills the
window. Your message is printed right under the previous answer, and the answer
streams below it; the input is back at the bottom when it is done. Everything
stays on the normal screen, so the scrollback keeps the whole conversation.
`/new` and `/resume` start the window over the same way. Under the input: the
directory and its git branch on the left, the key and the server (and what is
on, such as `think on`) on the right.

## Editor

| Key | Action |
|---|---|
| Enter | Send |
| Shift+Enter, Ctrl+J, Alt+Enter, or a line ending in `\` | New line |
| ↑ / ↓ | Move between lines; on the first or last line, browse earlier messages |
| Ctrl+G | Edit the message in `$VISUAL` / `$EDITOR` (vi by default) |
| Ctrl+A / Ctrl+E, Home / End | Start / end of the line |
| Alt+← / Alt+→ (Ctrl+← / Ctrl+→) | Previous / next word |
| Ctrl+W, Alt+Backspace | Delete the previous word |
| Ctrl+U / Ctrl+K | Delete to the start / end of the line |
| Ctrl+C | Stop the answer, or clear the input; twice on an empty input quits |
| Ctrl+D | Quit |

Shift+Enter needs a terminal that can tell it from Enter: one with the kitty
keyboard protocol or xterm's `modifyOtherKeys`, such as kitty, Ghostty,
iTerm2, WezTerm, foot, Alacritty or xterm. The input asks for both while it is
open and turns them off when it closes. Terminal.app sends a plain Enter for
Shift+Enter: use Ctrl+J there. In tmux, add `set -g extended-keys on` to
`~/.tmux.conf` and restart tmux; until then the header shows `ctrl+j` instead
of `shift+enter`.

A long paste (over 10 lines or 1000 characters) shows as
`[paste #1 +200 lines]` until it is sent. The marker is one piece: the cursor
never stops inside it, and deleting any part of it deletes all of it.

Your messages are kept in `~/.docsgpt/history` for ↑; lines that look like
keys or tokens are left out.

## Slash commands

Type `/` to see them, filtered as you type. Tab completes, Enter runs.

| Command | Action |
|---|---|
| `/new` (`/clear`) | Start a new conversation |
| `/resume` | Pick an earlier chat in this directory |
| `/copy` | Copy the last answer, or one of its code blocks |
| `/export [file]` | Save the conversation as markdown (default `docsgpt-<date>.md`; `~/` is your home directory; asks before overwriting a file, No by default) |
| `/think` | Show or hide the model's reasoning |
| `/key` | Switch to another stored key, or add one |
| `/settings` | Open the settings menu |
| `/help` | Show commands and keys |
| `/quit` (`/exit`) | Leave |

A line starting with `/` that is not a command, such as a path
(`/etc/hosts is empty, why?`), is sent as a message.

## Shell commands

`!command` runs a command on your machine, shows its output, and sends that
output along with your next message. `!!command` runs it without sending
anything. These run without approval and without a time limit, since you typed
them.

## Sessions

Every chat is saved under `~/.docsgpt/sessions/<directory>-<hash>/`, one JSONL
file per chat (readable only by you): `<directory>` is the last 48 characters
of the working directory's path, made file-name safe, and `<hash>` tells apart
paths that read the same. Sessions belong to the directory you started them
in; the list shows only the chats recorded for it.

- `docsgpt-cli -c` continues the latest chat of this directory.
- `docsgpt-cli -r` or `/resume` lists them (type to filter) with age, length and
  key; the last few exchanges are shown again.
- A resumed chat goes on in the same server conversation when the key and
  server are the same. Otherwise it continues in a new conversation, with the
  saved messages as history.
- When you leave, the CLI reminds you of `docsgpt-cli -c`.

## Stopping

Ctrl+C stops the answer, or the command the agent (or `!cmd`) is running, and
leaves you in the chat. A `TERM` or `HUP` signal (`kill`, `timeout`, a closed
terminal) stops it the same way and ends the chat, also while you type or pick
from a menu (nothing half-typed is sent). Either way the terminal is restored
(echo on) before the CLI exits. Exit codes follow the
shell's: `130` for Ctrl+C in a one-shot question, `143` for `TERM`, `129` for
`HUP`.

## Context

The first message of a conversation carries a short `<context>` block, so the
agent knows where you are:

| What | Setting | Default |
|---|---|---|
| The working directory | `send_current_directory` | on |
| Its first 50 entries | `send_directory_contents` | on |
| `AGENTS.md` (else `CLAUDE.md`) of every directory from the repository root down to the working directory, 12 KB in all | `send_project_instructions` | on |
| Your last shell commands (zsh, bash, fish) | `send_last_commands`, `number_of_last_commands` | off, 3 |

The block is sent again only when it changes. `--no-context` sends none of it
(the tools stay available). Change the settings with `/settings`,
`docsgpt-cli config`, or see [Configuration](configuration.md).

## Flags

| Flag | Effect |
|---|---|
| `-c`, `--continue` | Continue the latest chat in this directory |
| `-r`, `--resume` | Pick a chat to resume |
| `--no-stream` | Print each answer once it is complete |
| `--no-context` | Don't send the context block |
| `--no-tools` | Don't let the agent run commands or read and write files |
| `--auto-approve` | Run the agent's tool calls without asking |
| `--tool-timeout <s>` | Seconds a command run by the agent may take (default 30; formerly `--timeout`) |
| `--key <name>` | Chat with this stored key |
| `--url <url>` | Use this server |

The same flags (except `-c` and `-r`) work for one-shot questions, which also
take `--no-stdin` ([Ask once](quickstart.md#4-ask-once)).

## Display

- The dino banner shows in interactive chats only: `once` by default,
  `config set banner always|once|never`.
- Colours follow the terminal background (`config set theme auto|dark|light`);
  `NO_COLOR` turns them off.
- Answers are cleaned of terminal control sequences before they are drawn, so
  a model cannot recolour, retitle or clear your terminal or write to your
  clipboard. The same goes for resumed chats and `/export` files.
