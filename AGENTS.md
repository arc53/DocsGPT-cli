# DocsGPT-cli

Go CLI tool for interacting with the DocsGPT API from the terminal (v1.0.0).

## Project structure

```
main.go              → Entry point, calls cmd.Execute()
cmd/
  root.go            → Cobra root command, global flags (--url, --key, --token, --no-stream, --no-context, --auto-approve, --timeout)
  ask.go             → Single-shot Q&A with streaming + tool support
  chat.go            → Interactive multi-turn chat REPL with tool support
  config.go          → Config management (set-url, show)
  keys.go            → API key management (add/delete/set default)
  install.go         → Cross-platform install to system PATH
  update.go          → Self-update to latest GitHub release (--check, --yes, --rollback, hidden --worker)
  bench.go           → Benchmark suites vs agents (bench / bench record / bench init; --model, --matrix, --run-tag, --agent-id)
  manage.go          → Shared plumbing of the account-level (PAT) commands: client construction, exit codes (0/1/2), banner/usage suppression, confirmations
  login.go           → login / logout / whoami (personal access token in config.json)
  agents.go          → agents list / export / plan / apply / delete (agents as code)
  sources.go         → sources list / upload / delete, prompts list, tools list
  utils.go           → printError, extractCommand, copyToClipboard
internal/
  api/
    types.go         → OpenAI-compatible request/response types (Message, ChatRequest, ChatResponse, Delta, Tool, ToolCall)
    client.go        → HTTP client: Send (sync), SendStream (SSE), RunWithTools (tool call loop)
  config/
    config.go        → Unified config load/save/migrate from ~/.docsgpt/config.json; token/URL resolution (flag > env > config), token redaction
  manage/
    client.go        → PAT HTTP client (Bearer dgpt_pat_…, docsgpt-cli/<ver> User-Agent, context timeouts), typed APIError (message, error code, required_scope)
    agents.go        → /api/user/me, get_agents, export_agent, import_agent/plan + import_agent (Plan, Resolution, ApplyResult), delete_agent
    sources.go       → /api/sources, /api/upload (multipart, Idempotency-Key, deterministic default key), /api/task_status polling with backoff, delete_old
    catalog.go       → get_prompts, get_tools
    documents.go     → -f expansion: files / directories / stdin, verbatim multi-document YAML splitting, kind: Agent validation
    resolve.go       → --resolve parsing, mapping onto the server `resolution` object, missing/unavailable gating
  context/
    enricher.go      → Context building: cwd, dir contents, shell history
  display/
    renderer.go      → StreamDelta: prints content + reasoning tokens (dim)
  tools/
    definitions.go   → Tool schemas: run_command, read_file, write_file
    executor.go      → Local tool execution with timeout
    approval.go      → User approval prompt: [A]pprove [D]eny [E]dit
    safety.go        → Command blocklist, output truncation (10KB)
  update/
    update.go        → GitHub latest-release lookup, semver comparison, mode constants
    apply.go         → Asset download, sha256 verify, binary swap + backup, Rollback, host CheckAndApply
    stage.go         → Staged updates in ~/.docsgpt/staging (download now, apply next launch)
    worker.go        → Detached background worker (`update --worker`): check + stage
    notify.go        → Check state in ~/.docsgpt/update_check.json (latest, skip version)
    restart_*.go     → Post-update restart: exec(2) on Unix, exit(3) on Windows
    detach_*.go      → Platform detach for the worker process
```

## How it works

### ask command
1. Loads config from `~/.docsgpt/config.json`
2. Resolves API key (Bearer auth) and base URL
3. Optionally enriches question with context (cwd, dir listing, shell history)
4. Sends to `POST {base_url}/v1/chat/completions` with streaming
5. Handles tool calls (run_command, read_file, write_file) with user approval loop
6. Extracts bash/sh code blocks and copies to clipboard

### chat command
Interactive REPL with multi-turn conversation history. Same API + tool support.
Special commands: `/quit`, `/clear`, `/copy`.

### Auto-update flow
Modes via `settings.auto_update` ("on" default / "notify" / "off", `config set-auto-update`); env kill switch `DOCSGPT_NO_UPDATE_CHECK`.
1. On TTY launches, `updateGate` in root.go decides the mode (skips dev builds, the update/host commands; Homebrew or unwritable installs downgrade on → notify)
2. A detached worker (`update --worker`) refreshes the release cache daily and, in "on" mode, downloads + sha256-verifies the new binary into ~/.docsgpt/staging
3. The next launch validates the staged manifest and swaps it in near-instantly; the old binary is kept in ~/.docsgpt/backup for `update --rollback`
4. Rollback records a skip version so auto-update won't reinstall it; a manual `update` clears the skip
5. Host daemons check every ~12h while idle (10 min boot delay), apply directly, then restart: exec(2) on Unix (same PID), exit(3) on Windows (Task Scheduler RestartOnFailure); all shipped service configs restart only on failure since a revoke exits 0
6. Everything is stamped release-version-only: `update` refuses "dev"/git-describe builds and Homebrew-managed binaries

### Personal access tokens (account-level commands)
1. A PAT (`dgpt_pat_…`) is created in the web app; the CLI only consumes one (`/api/user/tokens` is closed to tokens). Resolution: `--token` > `DOCSGPT_TOKEN` > `config.json` `token`; base URL: `--url` > `DOCSGPT_URL` > config. Tokens are only ever printed redacted (`config.RedactToken`, first 15 chars + `…`)
2. `login` reads the token from `--token`, piped stdin, or a hidden prompt, validates it with `GET /api/user/me` and stores it together with the base URL that validated it, whether that came from `--url`, `DOCSGPT_URL` or the config (config stays 0600). `whoami` prints user, token name, scopes and resource restrictions; `logout` removes the stored token
3. `internal/manage` sends `Authorization: Bearer <PAT>`; server failures become `*manage.APIError` — `{success:false,message}`, 401 `invalid_token`, 403 `insufficient_scope` (+ `required_scope`), `resource_not_allowed`, `not_available_to_tokens`
4. `agents plan|apply -f`: files, directories (`*.yaml`/`*.yml`, sorted, not recursive) and `-`; multi-document files are split textually (the server gets each document verbatim); non-`Agent` kinds are rejected before any request. ALL documents are planned first (`POST /api/import_agent/plan {"yaml"}`, scope `agents:write`); if any reference is `missing`/`unavailable` and not covered by `--resolve`, nothing is applied and the exit code is 1. Then `POST /api/import_agent {"yaml","resolution"}` per document, stopping at the first failure. `plan` = `apply --dry-run`
5. `--resolve <kind>:<selector>=<value>` → server `resolution`: `source:<name>=<id>` → `sources[name]`; `tool:<sel>=reuse:<id>|create|skip` and `tool:<sel>.secret.<field>=<v>` → `tools["tool-N"] = {decision, tool_id, secrets}`; `model:<display_name>=<api_key>` → `models[name] = {api_key}`. `source:…=skip` / `model:…=skip` are CLI-side acknowledgements (the server has no such decision; it just leaves the reference off) and are never sent. `<sel>` = `tool-N` or an unambiguous tool name/type; positional keys are refused across several documents; an entry matching nothing is a usage error
6. `sources upload`: multipart `user` (legacy, required by the server), `name`, repeated `file`, with an explicit Content-Length and streamed file bodies. Default `Idempotency-Key` = `docsgpt-cli-upload-` + sha256(name + sorted (basename, file sha256)), so CI retries dedupe; `--wait` polls `/api/task_status` (1s → 10s backoff, 503 = transient, progress on stderr) until SUCCESS / FAILURE / `--timeout`; the `deduplicated` task id sentinel is not polled. `--replace` (needs `--wait`) then deletes the caller's older same-named sources (never the new one, never team-shared, never without a reported `source_id`): the server resolves an agent's source name to the OLDEST match, so without it agents stay pinned to the first upload; `agents apply` must run after
7. Exit codes mirror bench: 0 ok, 1 failure/blocked/timeout, 2 usage or validation (`exitError` in cmd/manage.go, mapped in `Execute`). These commands skip the banner, never dump usage on runtime errors, print errors to stderr, and refuse destructive actions without `--yes` when stdin is not a terminal

### bench with a personal access token
The `stream` and `answer` targets can address an agent by id: `agent_id:` (suite or case; mutually exclusive with `agent:`) or `--agent-id` sends `agent_id` in the body plus `Authorization: Bearer <PAT>` (scope `chat:run`) instead of an agent `api_key`; attachment uploads and the `/api/models` pricing fetch carry the PAT too. `v1`/`webhook` reject `agent_id` with a clear error; api_key runs are unchanged.

### Tool call flow
1. CLI sends `tools` array in request
2. If model returns `finish_reason: "tool_calls"`, CLI shows approval prompt
3. On approve: executes locally, sends result back as `role: "tool"` message
4. Model continues with tool results — loop repeats until `finish_reason: "stop"`

## Config

Single file: `~/.docsgpt/config.json`

```json
{
  "base_url": "https://gptcloud.arc53.com",
  "default_key": "my-agent",
  "keys": { "my-agent": "abc-123-key" },
  "token": "dgpt_pat_…",
  "settings": {
    "send_current_directory": true,
    "send_directory_contents": true,
    "send_last_commands": true,
    "number_of_last_commands": 3,
    "auto_update": "on"
  }
}
```

`token` is optional (written by `login`, removed by `logout`, omitted when empty); the file is always saved with mode 0600. Environment overrides: `DOCSGPT_TOKEN`, `DOCSGPT_URL`.

Auto-migrates from old `~/.docsgpt-keys.json` + `~/.docsgpt-settings.json` on first run.

## Key dependencies

- `spf13/cobra` — CLI framework
- `fatih/color` — colored output
- `atotto/clipboard` — clipboard access
- `manifoldco/promptui` — interactive prompts (used by install)
- `minio/selfupdate` — atomic binary replacement for the update command
- `golang.org/x/mod/semver` — version comparison

## Build & run

```bash
go build -o docsgpt-cli
./docsgpt-cli --help
```

## Notes

- Releases: pushing a `v*` tag runs `.github/workflows/release.yml` → GoReleaser builds linux/darwin/windows (amd64+arm64) archives + checksums.txt with stable asset names
- SSE streaming parsed with stdlib bufio.Scanner (no external SSE lib)
- Shell history: zsh, bash, fish
- Cross-platform: Unix + Windows
