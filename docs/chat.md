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

The chat takes the whole window, like a full-screen program: a short header
opens the conversation (version, keys, and the `AGENTS.md` / `CLAUDE.md` files
the context sends), and the input stays at the bottom with a line under it: the
directory and its git branch on the left, the key and the server (and what is
on, such as `think on`) on the right. You can type while an answer streams in;
Enter then queues your message until the answer is done. Queued messages are
listed above the input; Alt+↑ takes them back into it to edit, while the
answer goes on. When you leave, the
conversation is printed to the terminal, so it ends up in its scrollback as
usual.

## Scrolling

The chat scrolls its own conversation, so the terminal's scrollbar has nothing
to show while it runs.

| Key | Action |
|---|---|
| Mouse wheel | Scroll |
| PgUp / PgDn | A page up / down |
| Shift+↑ / Shift+↓ | A line up / down |
| Ctrl+↑ / Ctrl+↓ | Your previous / next message |
| End, Ctrl+End | Back to the end (End when nothing is typed) |
| Home, Ctrl+Home | To the start (Home when nothing is typed) |

At the end, the view follows the answer as it streams. Scrolled back, it stays
where you put it, and the line above the input counts what came in meanwhile
(`↓ 12 new lines · end to jump`). Sending a message, or a question from the
chat (an approval), brings you back to the end. Resizing the window wraps
everything again at the new width.

### Selecting text

Drag over the conversation with the mouse to select text; when you let go it
is copied, and the line above the input says so (`Copied 312 characters`).
A double click selects a word (a path or a URL counts as one), a triple click
the whole line as it was written: a paragraph, a list item, a line of code.
Both copy at once. Dragging onto the top row or below the conversation
scrolls it while you hold the button. The selection stays highlighted until
the next click, Esc or a resize, and stays on its text while an answer
streams in.

What is copied is plain text: no colours, no indentation the chat adds (the
two columns before code, a message's padding, a quote's bar), no spaces at
the ends of lines. Lines the chat wrapped to fit the window are joined again,
so a paragraph pastes as one line and a long line of code as one line. Across
messages, the parts are a blank line apart. `/copy` copies the last answer,
or one of its code blocks.

The text goes to the system clipboard. Over SSH (`SSH_TTY` or
`SSH_CONNECTION` set), or when there is no system clipboard (Linux without
`xclip`, `xsel` or `wl-copy`), it goes to your terminal's clipboard instead,
through the OSC 52 sequence, up to about 75 KB of text. Most terminals take it
(in iTerm2, turn on *Applications in terminal may access clipboard* under
Settings → General → Selection; Terminal.app has no support for it). In tmux, add `set -g set-clipboard on` to
`~/.tmux.conf` so tmux passes it on to the terminal.

The terminal's own selection still works with a modifier: Shift-drag in most
terminals (kitty, WezTerm, Ghostty, Alacritty, Windows Terminal, GNOME
Terminal, xterm, and tmux), Option-drag in iTerm2, Fn-drag in Terminal.app.

`docsgpt-cli config set mouse off` (or `/settings`) leaves the mouse to the
terminal: its selection works as usual and the keys above still scroll. The
wheel then does whatever the terminal does on a full-screen program: many send
↑ and ↓, which browse your earlier messages in the input.

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
| Ctrl+- | Undo: a word, a run of deletions, a paste or a recalled message at a time |
| Alt+↑ | Take the queued messages back into the input (the answer goes on) |
| Esc | Clear the selection; else stop the answer or the command that runs |
| Ctrl+C | Stop the answer, or clear the input; twice on an empty input quits |
| Ctrl+D | Quit |
| Ctrl+Z | Suspend (`fg` to come back) |
| Ctrl+L | Redraw the window |

Shift+Enter needs a terminal that can tell it from Enter: one with the kitty
keyboard protocol or xterm's `modifyOtherKeys`, such as kitty, Ghostty,
iTerm2, WezTerm, foot, Alacritty or xterm. The chat asks for both while it
runs and turns them off when it ends. Terminal.app sends a plain Enter for
Shift+Enter: use Ctrl+J there. In tmux, add `set -g extended-keys on` to
`~/.tmux.conf` and restart tmux; until then the header shows `ctrl+j` instead
of `shift+enter`.

A long paste (over 10 lines or 1000 characters) shows as
`[paste #1 +200 lines]` in the input. The marker is one piece: the cursor
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
| `/approve` | Run tool calls without asking (the footer then shows `auto-approve`), or ask again; see [Tools](tools.md#always-approve) |
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
  key; the conversation is shown again.
- A resumed chat goes on in the same server conversation when the key and
  server are the same. Otherwise it continues in a new conversation, with the
  saved messages as history.
- When you leave, the CLI reminds you of `docsgpt-cli -c`.

## Stopping

Esc or Ctrl+C stops the answer, or the command the agent (or `!cmd`) is
running, and leaves you in the chat; messages you queued meanwhile go back into
the input. Esc or Ctrl+C at an approval stops the whole answer. A `TERM` or
`HUP` signal (`kill`, `timeout`, a closed terminal) ends the chat, also while
you type or pick from a menu (nothing half-typed is sent). Either way the
terminal is restored before the CLI exits. Exit codes follow the shell's: `130`
for Ctrl+C in a one-shot question, `143` for `TERM`, `129` for `HUP`.

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
