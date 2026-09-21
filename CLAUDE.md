# DocsGPT-cli

Go CLI tool for interacting with the DocsGPT API from the terminal (v1.0.0).

## Project structure

```
cmd/docsgpt-cli/     → Entry point (package main), calls cmd.Execute(). Lives here
                       so `go install .../cmd/docsgpt-cli@latest` names the binary
                       docsgpt-cli rather than DocsGPT-cli (the module path's last
                       element)
sdk/                 → SEPARATE Go module github.com/arc53/DocsGPT-cli/sdk, package
                       docsgpt: the public chat client (Client, Send, SendStream,
                       RunWithTools, StreamHandler, APIError) — OpenAI-compatible
                       types and the tool-call loop. Stdlib-only, tagged sdk/vX.Y.Z
                       independently of the CLI, currently pre-v1. The CLI depends
                       on it through a pinned require in go.mod (imported as
                       `docsgpt "…/sdk"`, since the package name is not the last
                       path element); go.work points local builds at ./sdk, and
                       release builds set GOWORK=off to use the pinned version
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
  config/
    config.go        → Unified config load/save/migrate from ~/.docsgpt/config.json; token/URL resolution (flag > env > config), token redaction
  bench/
    spec/            → Suite/case YAML format (bench.yaml + case.yaml), loading, validation, golden files; model/stream/attachments_mode/turns/expect.error/expect.stream
    assert/          → Assertion engine: answer/json (gjson paths)/sources/tools/limits (incl. TTFT)/stream integrity/error (negative cases)/golden matchers
    target/          → Four wire protocols: v1 (/v1/chat/completions, JSON or SSE, inline file parts), stream (/stream SSE, TTFT + frames), answer (/api/answer), webhook (+ /api/task_status polling); attachment upload; ServerError for negative cases
    judge/           → LLM-as-judge grading via a second agent (model/temperature passthrough)
    pricing/         → Cost table: /api/models pricing (tolerant field reader) + bench.yaml pricing overrides
    runner/          → Worker pool, repeat/min_pass, fail-fast, golden record, judge wiring, multi-turn loop, model override, cost stamping
    report/          → Pretty/JSON(schema 2)/JUnit output, A/B compare, --matrix table + JSON, baselines in ~/.docsgpt/bench/<suite>/
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
Special commands: `/quit`, `/clear`, `/copy`, `/think`. Ctrl+C cancels the in-flight
request (signal.NotifyContext in the executor; the prompt library restores cooked mode
around the executor so it is a real SIGINT) or clears the input line; Ctrl+D exits.
Tool approval reads stdin with a CR/LF-tolerant reader (internal/tools/approval.go).

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
2. Per case: uploads attachments (waits for the extraction task) or, with `attachments_mode: inline` (v1 only), base64-embeds them as content parts; asks the agent through the case's target (`v1`/`stream`/`answer`/`webhook`), evaluates `expect` assertions
3. Model selection: `model:` (suite/case) or `--model` → `model_id` on stream/answer, `model` on v1; stamped into every result. NOTE: the server currently ignores `model_id` for agent-bound (api_key) requests — honoring it is a docsgpt-cloud change
4. Multi-turn `turns:` — `conversation_id` carried on stream/answer, messages replayed on v1; per-turn `expect` optional, case `expect` grades the last turn; timeout per turn, `limits.max_seconds` whole case; judge sees the transcript
5. Assertions: answer text (contains/regex/…), JSON fields via gjson paths, sources count, tool calls, LLM-as-judge rubric (`judge.model`/`temperature` forwarded, verdicts recorded), latency/TTFT/token limits, `expect.stream` SSE integrity (stream target), `expect.error` negative cases (server error expected; success fails), golden snapshots (`bench record`)
6. Repeat/min_pass for flaky LLMs, `--vs` A/B, `--matrix m1,m2` per-model runs + comparison table (JSON keyed results[model][case]), `--baseline last` regression diff, `--json`/`--junit` for CI; exit codes 0/1/2
7. Cost: `/api/models` fetched once per base URL (tolerant pricing reader) merged with `bench.yaml pricing:`; `cost_usd` per run when usage (v1) and a price for the effective model exist
8. Webhook target sends `{"question": ...}` — the server passes the serialized JSON verbatim as the agent query; no attachments/turns there, approval-gated tools auto-denied
9. Secrets: YAML values support `${VAR}` interpolation (shell env > suite/.env > ./.env, `$$` escape, comment lines exempt, unset var = load error); `--webhook-url` injects the webhook token without YAML
10. Hygiene: `--run-tag`/`run_tag:` → `X-DocsGPT-Bench-Tag: bench:<tag>` + `docsgpt-cli/<ver> bench` User-Agent on target and judge requests (uploads/polls carry the User-Agent)
11. Agent by id: `agent_id:` (suite or case; mutually exclusive with `agent:` at the same level, the case level wins as a whole) or `--agent-id` → stream/answer send `agent_id` in the body + `Authorization: Bearer <PAT>` (scope `chat:run`) instead of `api_key`; attachments upload (and its task polling) and the `/api/models` pricing fetch carry the PAT too (pricing retries anonymously on 401/403). The PAT is only ever sent to the origin the user configured (`--url`, else `DOCSGPT_URL`/config; `spec.SameOrigin`): an `agent_id` case whose YAML `base_url` points elsewhere is a case error, and pricing for such a URL is fetched anonymously. `v1`/`webhook` + `agent_id` is a load/run error; `agent_id` without a token is a case error; api_key runs are byte-for-byte unchanged

### Personal access tokens (account-level commands)
1. A PAT (`dgpt_pat_…`) is created in the web app; the CLI only consumes one (`/api/user/tokens` is closed to tokens). Resolution: `--token` > `DOCSGPT_TOKEN` > `config.json` `token`; base URL: `--url` > `DOCSGPT_URL` > config. Tokens are only ever printed redacted (`config.RedactToken`, first 15 chars + `…`)
2. `login` reads the token from `--token`, piped stdin, or a hidden prompt, validates it with `GET /api/user/me` and stores it together with the base URL that validated it, whether that came from `--url`, `DOCSGPT_URL` or the config (config stays 0600). `whoami` prints user, token name, scopes and resource restrictions; `logout` removes the stored token
3. `internal/manage` sends `Authorization: Bearer <PAT>`; server failures become `*manage.APIError` — `{success:false,message}`, 401 `invalid_token`, 403 `insufficient_scope` (+ `required_scope`), `resource_not_allowed`, `not_available_to_tokens`
4. `agents plan|apply -f`: files, directories (`*.yaml`/`*.yml`, sorted, not recursive) and `-`; multi-document files are split textually (the server gets each document verbatim); non-`Agent` kinds are rejected before any request. ALL documents are planned first (`POST /api/import_agent/plan {"yaml"}`, scope `agents:write`); if any reference is `missing`/`unavailable` and not covered by `--resolve`, nothing is applied and the exit code is 1. Then `POST /api/import_agent {"yaml","resolution"}` per document, stopping at the first failure. `plan` = `apply --dry-run`
5. `--resolve <kind>:<selector>=<value>` → server `resolution`: `source:<name>=<id>` → `sources[name]`; `tool:<sel>=reuse:<id>|create|skip` and `tool:<sel>.secret.<field>=<v>` → `tools["tool-N"] = {decision, tool_id, secrets}`; `model:<display_name>=<api_key>` → `models[name] = {api_key}`. `source:…=skip` / `model:…=skip` are CLI-side acknowledgements (the server has no such decision; it just leaves the reference off) and are never sent. `<sel>` = `tool-N` or an unambiguous tool name/type; positional keys are refused across several documents; an entry matching nothing is a usage error
6. `sources upload`: multipart `user` (legacy, required by the server), `name`, repeated `file`, with an explicit Content-Length and streamed file bodies. Default `Idempotency-Key` = `docsgpt-cli-upload-` + sha256(name + sorted (basename, file sha256)), so CI retries dedupe; `--wait` polls `/api/task_status` (1s → 10s backoff, 503 = transient, progress on stderr) until SUCCESS / FAILURE / `--timeout`; the `deduplicated` task id sentinel is not polled. `--replace` (needs `--wait`) then deletes the caller's older same-named sources (never the new one, never team-shared, never without a reported `source_id`, and never unless that id is in the current listing: a content revert repeats the Idempotency-Key, and the deduplicated reply then names the earlier, already deleted source; the command fails with exit 1 instead of deleting the only live one): the server resolves an agent's source name to the OLDEST match, so without it agents stay pinned to the first upload; `agents apply` must run after
7. Exit codes mirror bench: 0 ok, 1 failure/blocked/timeout, 2 usage or validation (`exitError` in cmd/manage.go, mapped in `Execute`). These commands skip the banner, never dump usage on runtime errors, print errors to stderr, and refuse destructive actions without `--yes` when stdin is not a terminal

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

`token` is optional (written by `login`, removed by `logout`, omitted when empty); the file is always written as a 0600 temp file and renamed into place (atomic, never readable by others, tightens an older permissive file). Environment overrides: `DOCSGPT_TOKEN`, `DOCSGPT_URL`.

Auto-migrates from old `~/.docsgpt-keys.json` + `~/.docsgpt-settings.json` on first run.

## Key dependencies

- `spf13/cobra` — CLI framework
- `charmbracelet/glamour` + `lipgloss` — markdown rendering and styling
- `atotto/clipboard` — clipboard access
- `elk-language/go-prompt` — interactive chat prompt (maintained fork of c-bata/go-prompt; the original never restores the terminal after raw mode, which broke the tool-approval prompt and Ctrl-C inside `chat`)
- `minio/selfupdate` — atomic binary replacement for the update command
- `golang.org/x/mod/semver` — version comparison
- `gopkg.in/yaml.v3` — bench suite/case files
- `tidwall/gjson` — JSON path assertions in bench

## Build & run

```bash
go build -o docsgpt-cli ./cmd/docsgpt-cli
./docsgpt-cli --help
```

## Notes

- Module path is `github.com/arc53/DocsGPT-cli` (mixed case, matching the repo; the Go proxy escapes it as `!docs!g!p!t-cli`, which users never type). `go.work` is committed and spans `.` and `./sdk`
- `cmd.Version` is stamped by ldflags for release and `make build`; `resolveVersion` in root.go recovers it from `debug.ReadBuildInfo` for `go install` builds, which carry no ldflags and would otherwise report "dev" and disable their own update checks
- Releases: `.github/workflows/release.yml` (dispatch, or a hand-pushed `v*` tag) → GoReleaser builds linux/darwin/windows (amd64+arm64) archives + checksums.txt with stable asset names, attaches `deployment/install.sh`/`install.ps1`, and commits a Homebrew **cask** to `arc53/homebrew-DocsGPT-cli` with `HOMEBREW_TAP_TOKEN` (`skip_upload: auto` keeps prereleases out of brew; the job only runs on `arc53/DocsGPT-cli`). Cut from Actions → Release → Run workflow (a `cli`/`sdk` bump input; one run tags the sdk, pushes the `go.mod` pin bump, then tags and releases the CLI in that order) — there is no local release target; see `RELEASING.md`
- Install script: `docs.ac/install-cli` redirects to `releases/latest/download/install.sh`, so the live installer is whatever the newest release carries — a fix lands only on the next tag. Both installers verify the archive against `checksums.txt`, then hand off to `docsgpt-cli install`, which owns the PATH logic for every platform
- SSE streaming parsed with stdlib bufio.Scanner (no external SSE lib)
- Shell history: zsh, bash, fish
- Cross-platform: Unix + Windows
