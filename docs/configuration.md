# Configuration

Everything lives in `~/.docsgpt/`:

| Path | What |
|---|---|
| `config.json` | Server, keys, token, settings |
| `history` | Your chat prompts, for ↑ |
| `sessions/` | Saved chats, per directory |
| `bench/` | Bench run history |
| `host.yml`, `host.key`, `host.log` | [Host mode](host.md) pairing and log |

`config.json` is written atomically and readable only by you (mode `0600`).
The old pair of files (`~/.docsgpt-keys.json`, `~/.docsgpt-settings.json`) is
migrated on the first run.

```json
{
  "base_url": "https://gptcloud.arc53.com",
  "default_key": "support",
  "keys": { "support": "…" },
  "token": "dgpt_pat_…",
  "settings": {
    "send_current_directory": true,
    "send_directory_contents": true,
    "send_project_instructions": true,
    "send_last_commands": false,
    "number_of_last_commands": 3,
    "auto_update": "on"
  }
}
```

## Credentials

There are two kinds:

| Credential | Used for | Where to get it |
|---|---|---|
| Agent API key | Chatting with one agent, bench | DocsGPT → Agent settings → API key |
| Personal access token (`dgpt_pat_…`) | `agents`, `sources`, bench `agent_id` runs; acts as you, limited to its scopes | DocsGPT → Settings → Access tokens |

`docsgpt-cli login` takes either one: from a hidden prompt on a terminal, from
stdin otherwise, or the token from `--token`. It checks the credential with
the server before saving it, together with the server URL it was checked on.

```bash
docsgpt-cli login                                       # pick the default key, or add one
echo "$AGENT_KEY" | docsgpt-cli login --name support    # add a key (becomes the default)
echo "$DOCSGPT_TOKEN" | docsgpt-cli login               # store a token
docsgpt-cli whoami                                      # active key and its agent; token user, scopes, restrictions
docsgpt-cli logout                                      # pick what to remove
docsgpt-cli logout support --yes
docsgpt-cli logout --token --yes
docsgpt-cli logout --all --yes
```

`whoami` exits 1 when nothing is configured (`--json`: no token), so a CI
step can check its credentials with it.

`logout` only forgets a credential locally; revoke it in DocsGPT to invalidate
it. Without a terminal, `logout` needs `--yes`. `logout --token` takes no
value; given one (`logout --token dgpt_pat_…`), it removes the stored token
only if that is the one. The CLI never prints a full
token or key: `dgpt_pat_AbCdEf…`, `a1b2…c3d4`.

The config holds one server. Logging in with `--url` moves it, unless a stored
token belongs to the old server; the CLI then keeps the server and says how to
use the key with `--url`.

### Which one is used

| What | Order |
|---|---|
| Agent key | `--key <name>` > `DOCSGPT_API_KEY` > `default_key` |
| Token | `--token` > `DOCSGPT_TOKEN` > `token` |
| Server | `--url` > `DOCSGPT_URL` > `base_url` > `https://gptcloud.arc53.com` |

CI jobs can set the variables instead of logging in.

## Environment variables

| Variable | Effect |
|---|---|
| `DOCSGPT_API_KEY` | Agent API key to chat with (the key itself) |
| `DOCSGPT_TOKEN` | Personal access token |
| `DOCSGPT_URL` | Server URL |
| `DOCSGPT_WEBHOOK_URL` | Default webhook for `agents trigger` |
| `DOCSGPT_NO_UPDATE_CHECK` | Turns off every update check and auto-update |
| `NO_COLOR` | No colours |
| `VISUAL`, `EDITOR` | Editor for Ctrl+G in the chat |
| `DOCSGPT_CLI_VERSION`, `DOCSGPT_NO_MODIFY_PATH` | For the [install scripts](install.md) |

## Settings

On a terminal, `docsgpt-cli config` (or `/settings` in a chat) opens a menu.
In scripts:

```bash
docsgpt-cli config show                       # settings, keys and token, redacted
docsgpt-cli config show --json
docsgpt-cli config get auto_update
docsgpt-cli config set url http://localhost:7091
docsgpt-cli config path                       # ~/.docsgpt/config.json
```

| Key | Values | Default |
|---|---|---|
| `url` | Server URL | `https://gptcloud.arc53.com` |
| `default_key` | Name of a stored key | the first key |
| `auto_update` | `on`, `notify`, `off` ([Updating](install.md#updating)) | `on` |
| `banner` | `always`, `once`, `never` | `once` |
| `theme` | `auto`, `dark`, `light` | `auto` |
| `mouse` | `on`, `off`: scroll the chat with the wheel and select text to copy it ([Scrolling](chat.md#scrolling), [Selecting text](chat.md#selecting-text)) | `on` |
| `send_current_directory` | `true`, `false` | `true` |
| `send_directory_contents` | `true`, `false` | `true` |
| `send_project_instructions` | `true`, `false` (AGENTS.md / CLAUDE.md) | `true` |
| `send_last_commands` | `true`, `false` | `false` |
| `number_of_last_commands` | `0`–`50` | `3` |

The `send_*` settings shape the [chat context](chat.md#context).

## Global flags

Every command takes:

| Flag | Effect |
|---|---|
| `--url <url>` | Server URL |
| `--key <name>` | Stored agent key to use |
| `--token <token>` | Personal access token |

## Older commands

These still work but are hidden from help: `ask "question"` and `chat` (now
plain `docsgpt-cli`), `keys` (now `login`), `config
set-url|set-theme|set-banner|set-auto-update <value>` (now `config set`),
`prompts list` and `tools list` (now `agents prompts` and `agents tools`), and
`--timeout` on `ask`/`chat` (now `--tool-timeout`).
