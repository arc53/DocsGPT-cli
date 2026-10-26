# MCP servers

MCP servers (Model Context Protocol) give the agent more tools: Laravel
Boost's documentation search and database queries, a browser, an issue
tracker. The chat and one-shot questions start the servers you list, offer
their tools to the agent next to its own [tools](tools.md), and stop them when
they end. Each tool call is approved like a command.

Servers run on your machine as commands, talking over stdin and stdout
(stdio). Remote servers (a URL) are not supported yet.

## Configuring servers

Two files list servers, in the `mcpServers` format most MCP clients share:

| File | Servers |
|---|---|
| `~/.docsgpt/mcp.json` | Yours, in every directory |
| `.mcp.json` of the project | The project's: in the working directory, or the nearest directory above it up to the repository root |

```json
{
  "mcpServers": {
    "laravel-boost": {
      "command": "php",
      "args": ["artisan", "boost:mcp"]
    },
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_TOKEN}" }
    }
  }
}
```

| Field | What |
|---|---|
| `command` | The program to run (looked up on `PATH`, or a path relative to the directory below) |
| `args` | Its arguments |
| `env` | Environment variables added to yours |
| `cwd` | The directory it runs in, relative to the file. By default a project's servers run in the directory of its `.mcp.json`, yours in the working directory |
| `disabled` | `true` leaves the server out |

`${VAR}` in a command, an argument, a value of `env` or `cwd` is replaced
with the environment variable, and `${VAR:-default}` with the default when the
variable is unset or empty. A server using a variable that is not set does not
start. Keep secrets in variables, not in a project's file.

When both files name a server, the project's entry is used.

### Laravel Boost

`composer require laravel/boost --dev`, then `php artisan boost:install`. It
writes Boost's guidelines into `AGENTS.md`, which the agent reads with the
[context](chat.md#context), and the MCP config of the agents you pick, one of
which is `.mcp.json`. Or add the entry above yourself. Boost runs only where `APP_ENV` is `local` (or debug is on), from the
project's root, which is where a project's server starts. With Sail, the
command is `vendor/bin/sail` and the arguments `["artisan", "boost:mcp"]`.

## Approving a project's servers

A project's `.mcp.json` comes with the repository, and each server in it is a
command. The first time a chat (or a question asked on a terminal) finds one,
it shows the command, its `env` and `cwd` as the file writes them (variables
not filled in), and the directory it runs in, and asks:

```
$ php artisan boost:mcp (MCP server laravel-boost of .mcp.json)
in .
→ Start    Not now    Never
```

| Choice | Key | Effect |
|---|---|---|
| Start | `s`, `y` | Start it, and from now on without asking while its entry stays the same |
| Not now | `n` | Don't start it this time; ask again next time (Esc does the same) |
| Never | `v` | Don't start it, and don't ask again |

The answer is kept in `~/.docsgpt/mcp-approvals.json` for that file and server
name. When the entry changes (another command, argument or variable), it is
asked about again. `docsgpt-cli mcp reset` forgets the answers about the
project's servers. Without a terminal (a pipeline with `--auto-approve`), a
server not approved yet is not started, and a note on stderr says so. Your own
servers in `~/.docsgpt/mcp.json` start without asking.

## Tools

The agent gets each server's tools as `mcp__<server>__<tool>`
(`mcp__laravel-boost__search-docs`): characters other than letters, digits,
`_` and `-` become `_`, and a name too long or already taken is shortened and
given a short hash. The [context](chat.md#context) block lists the servers
with their tools, and what each server says about itself, so instructions that
name a tool without the prefix ("use `search-docs`") still work.

A call shows as a block: the server and tool, `(MCP)` or `(MCP, read-only)`
when the server marks the tool as one that changes nothing, and the arguments.
It asks first, unless you allowed it:

```
laravel-boost · search-docs (MCP, read-only)
queries: ["eloquent scopes"]
→ Approve    Always allow search-docs    Always allow laravel-boost    Always approve    Deny
```

| Choice | Key | Effect |
|---|---|---|
| Approve | `a`, `y` | Call it once |
| Always allow *tool* | `l` | Call this tool without asking in this project from now on |
| Always allow *server* | `s` | Call any tool of this server without asking in this project from now on |
| Always approve | `p` | Run every later tool call in this project without asking |
| Deny | `d`, `n` | Don't call it; the model is told you declined |

`--auto-approve`, "Always approve" and `/approve` cover MCP tools too. The
read-only mark is the server's claim, so it changes nothing about the
approval.

The model gets the result's text, cut to its first 2000 lines or 50 KB (with a
note saying so), and its images (up to 20 MB each), as `read_file` sends them.
Audio and binary resources are named, not sent. A result the server marks as
an error shows `✗ error` and reaches the model as one. A call may take up to 5
minutes; Esc or Ctrl+C stops it.

## Starting and stopping

Servers start in the background as the chat opens; the first message waits
for those still starting (`Starting MCP servers…`, Esc sends it without them),
at most 30 seconds each. A one-shot question waits for them before it asks. A
server that fails to start is reported once, with what it last wrote, and the
chat goes on without it.

Each server runs in a process group of its own, without your terminal: it
cannot read your keys, and when the chat ends its input is closed, then it is
stopped with everything it started (`npx` and `uvx` leave child processes).
A server that stops during a chat is started again for the next call; a call
it was running is not repeated, as it may have acted.

What a server writes to stderr, and anything it prints on stdout that is not
the protocol (a banner, a PHP notice), goes to
`~/.docsgpt/logs/mcp-<server>.log` (started over past 1 MB).

`--no-tools` starts no server.

## Checking the servers

```
$ docsgpt-cli mcp list
✓ laravel-boost · 10 tools · .mcp.json
    application-info, browser-logs, database-connections, database-query, …
✗ github · ~/.docsgpt/mcp.json
    GITHUB_TOKEN is not set
- other · not approved yet: a chat here asks · .mcp.json
```

`mcp list` starts the servers the chat would start in this directory, lists
their tools and stops them; it exits 1 when one fails. In a chat, `/mcp` shows
the same for the servers it runs.
