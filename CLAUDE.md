# DocsGPT-cli

Go CLI tool for interacting with the DocsGPT API from the terminal (v1.0.0).

## Project structure

```
main.go              → Entry point, calls cmd.Execute()
cmd/
  root.go            → Cobra root command, global flags (--url, --key, --no-stream, --no-context, --auto-approve, --timeout)
  ask.go             → Single-shot Q&A with streaming + tool support
  chat.go            → Interactive multi-turn chat REPL with tool support
  config.go          → Config management (set-url, show)
  keys.go            → API key management (add/delete/set default)
  install.go         → Cross-platform install to system PATH
  update.go          → Self-update to latest GitHub release (--check, --yes, --rollback, hidden --worker)
  bench.go           → Benchmark suites vs agents (bench / bench record / bench init)
  utils.go           → printError, extractCommand, copyToClipboard
internal/
  api/
    types.go         → OpenAI-compatible request/response types (Message, ChatRequest, ChatResponse, Delta, Tool, ToolCall)
    client.go        → HTTP client: Send (sync), SendStream (SSE), RunWithTools (tool call loop)
  config/
    config.go        → Unified config load/save/migrate from ~/.docsgpt/config.json
  bench/
    spec/            → Suite/case YAML format (bench.yaml + case.yaml), loading, validation, golden files
    assert/          → Assertion engine: answer/json (gjson paths)/sources/tools/limits/golden matchers
    target/          → Three wire protocols: v1 (/v1/chat/completions), stream (/stream SSE), webhook (+ /api/task_status polling); attachment upload
    judge/           → LLM-as-judge grading via a second agent
    runner/          → Worker pool, repeat/min_pass, fail-fast, golden record, judge wiring
    report/          → Pretty/JSON(schema 1)/JUnit output, A/B compare, baselines in ~/.docsgpt/bench/<suite>/
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

### bench command
1. Loads a suite dir (default `./bench`): optional `bench.yaml` defaults + any subdir with a `case.yaml`
2. Per case: uploads attachments (waits for the extraction task), asks the agent through the case's target (`v1`/`stream`/`webhook`), evaluates `expect` assertions
3. Assertions: answer text (contains/regex/…), JSON fields via gjson paths, sources count, tool calls, LLM-as-judge rubric, latency/token limits, golden snapshots (`bench record`)
4. Repeat/min_pass for flaky LLMs, `--vs` A/B, `--baseline last` regression diff, `--json`/`--junit` for CI; exit codes 0/1/2
5. Webhook target sends `{"question": ...}` — the server passes the serialized JSON verbatim as the agent query; no attachments there, approval-gated tools auto-denied
6. Secrets: YAML values support `${VAR}` interpolation (shell env > suite/.env > ./.env, `$$` escape, comment lines exempt, unset var = load error); `--webhook-url` injects the webhook token without YAML

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
  "settings": {
    "send_current_directory": true,
    "send_directory_contents": true,
    "send_last_commands": true,
    "number_of_last_commands": 3,
    "auto_update": "on"
  }
}
```

Auto-migrates from old `~/.docsgpt-keys.json` + `~/.docsgpt-settings.json` on first run.

## Key dependencies

- `spf13/cobra` — CLI framework
- `charmbracelet/glamour` + `lipgloss` — markdown rendering and styling
- `atotto/clipboard` — clipboard access
- `c-bata/go-prompt` — interactive prompts
- `minio/selfupdate` — atomic binary replacement for the update command
- `golang.org/x/mod/semver` — version comparison
- `gopkg.in/yaml.v3` — bench suite/case files
- `tidwall/gjson` — JSON path assertions in bench

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
