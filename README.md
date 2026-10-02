# DocsGPT-CLI

DocsGPT-cli is a command-line interface (CLI) tool that allows you to interact with [DocsGPT](https://github.com/arc53/DocsGPT). Chat with your agents, ask one-off questions in pipelines, and manage agents and sources as code, from your terminal.

---

## Installation

### 1. Install script (macOS and Linux)

```bash
curl -fsSL https://docs.ac/install-cli | bash
```

On Windows, in PowerShell:

```powershell
irm https://docs.ac/install-cli.ps1 | iex
```

This downloads the release build for your platform, checks it against the
published `checksums.txt`, and puts it on your `PATH`. Run it again to upgrade.

To read the script before running it, download it first:
`curl -fsSL https://docs.ac/install-cli -o install.sh`, then `bash install.sh`.

Environment:

- `DOCSGPT_CLI_VERSION` — install a specific release instead of the latest (e.g. `v1.5.1`)
- `DOCSGPT_NO_MODIFY_PATH=1` — install the binary but leave shell profiles alone

### 2. Homebrew (macOS)

```bash
brew tap arc53/docsgpt-cli
brew install --cask docsgpt-cli
```

Upgrade with `brew upgrade --cask docsgpt-cli`. Homebrew-managed copies never
self-update, so `docsgpt-cli update` will point you back at brew.

> **Upgrading from a version installed before 1.6.0?** Those came from a
> formula, which is no longer updated. Move across once with:
>
> ```bash
> brew uninstall --formula --force docsgpt-cli && brew install --cask docsgpt-cli
> ```

Homebrew on Linux is not supported — casks are macOS-only. Use the install
script above instead.

### 3. Download the Binary

Download the latest archive for your platform from the
[Releases page](https://github.com/arc53/DocsGPT-cli/releases). You can run it
as is or use the `install` command to add the binary to your system's `PATH`:

```bash
./docsgpt-cli
./docsgpt-cli install
```

### 4. go install

```bash
go install github.com/arc53/DocsGPT-cli/cmd/docsgpt-cli@latest
```

Requires v1.6.0 or newer — earlier releases predate the module rename.

Installs into `$(go env GOBIN)` (or `$(go env GOPATH)/bin`). The binary knows
which release it came from and self-updates like any other, so you do not have
to re-run `go install` to stay current.

The path ends in `/cmd/docsgpt-cli` on purpose: `go install` names the binary
after the last element of the path, and installing the module root would
produce one called `DocsGPT-cli`.

### 5. Compile from Source

If you want to make adjustments or compile the binary yourself, clone the repository and compile it:

```bash
git clone https://github.com/arc53/docsgpt-cli.git
cd docsgpt-cli
make build
```

After compiling, follow the same steps as for the binary.

---

## Go SDK

The DocsGPT chat client the CLI is built on is published as its own Go module:

```go
import "github.com/arc53/DocsGPT-cli/sdk"

client := docsgpt.NewClient("https://gptcloud.arc53.com", apiKey)
resp, err := client.Send(ctx, docsgpt.ChatRequest{
	Messages: []docsgpt.Message{{Role: "user", Content: "What is DocsGPT?"}},
})
```

```bash
go get github.com/arc53/DocsGPT-cli/sdk
```

It has no dependencies beyond the standard library, and is versioned
independently of the CLI under `sdk/vX.Y.Z` tags. It is below v1, so the API
may still change.

---

## Usage

```bash
docsgpt-cli                                 # interactive chat
docsgpt-cli -c                              # continue the latest chat here
docsgpt-cli "how do I rotate the API key?"  # ask once and exit
git diff | docsgpt-cli "review this"        # send piped input along
docsgpt-cli login                           # add an agent API key
```

With no arguments on a terminal, `docsgpt-cli` opens a chat (`chat "first
message"` starts one with a message). With a question, or anything piped in, it
answers once and exits; when stdout is not a terminal it writes only the answer,
as plain text, so it fits in pipelines:

```bash
tail -n 50 app.log | docsgpt-cli "Why does this fail?"
docsgpt-cli "Summarize the README" > summary.md
```

With a question, piped input is read when it starts arriving within a second
(so an idle stdin under ssh or CI is left alone), and a redirected file only
from its start; input over 1 MB is cut.

A single word that looks like a mistyped command (`docsgpt-cli agnets`) is
rejected with a hint instead of being sent; `docsgpt-cli -- <words>` always sends
them as a question. `ask` and `chat` still work as before.

Chat flags: `--no-stream`, `--no-context` (don't send the working directory, its
files, AGENTS.md or shell history), `--no-tools` (don't let the agent run
commands or touch files), `--auto-approve` (run the agent's tool calls without
asking), `--tool-timeout <seconds>` (formerly `--timeout`), and `-c/--continue`
or `-r/--resume` to pick up an earlier chat. Every command
takes `--url`, `--key <name>` and `--token`; run `docsgpt-cli <command> --help`
for the rest.

### Chatting

Enter sends; Ctrl+J or Alt+Enter (or a line ending in `\`) starts a new line,
↑/↓ walk through earlier messages, Ctrl+G edits the message in `$EDITOR`, and a
long paste shows as `[paste #1 +200 lines]` until it is sent. Ctrl+C stops an
answer or clears the input; press it twice on an empty input, or Ctrl+D, to
quit. Type `/` for the commands:

| Command | |
|---|---|
| `/new` | start a new conversation (`/clear`) |
| `/resume` | pick an earlier chat in this directory |
| `/copy` | copy the last answer, or one of its code blocks |
| `/export [file]` | save the conversation as markdown |
| `/think` | show or hide the model's reasoning |
| `/key` | switch to another stored key (or add one) |
| `/settings` | change settings |
| `/help`, `/quit` | |

`!command` runs a shell command and sends its output along with your next
message; `!!command` runs it without sending anything.

Each chat starts with a short description of where you are: the working
directory, its first 50 entries, and the `AGENTS.md` (or `CLAUDE.md`) files from
the repository root down to it. Recent shell commands are sent only if you turn
on `send_last_commands`. Chats are saved under `~/.docsgpt/sessions/` and your
prompts in `~/.docsgpt/history` (lines that look like keys or tokens are left
out); `docsgpt-cli -c` continues the latest chat of the directory and `-r` lets
you pick one.

### Signing in

Copy an agent's API key from DocsGPT (**Agent settings → API key**). The first
time you chat, the CLI asks for it, checks it with the server and saves it; or
add it any time with `docsgpt-cli login`, which also takes a personal access
token (see below). On a terminal, `login` lists the stored keys to switch the
default one, `logout` lists them to remove one, and `whoami` shows the active
key and its agent. Without a terminal:

```bash
echo "$AGENT_KEY" | docsgpt-cli login --name support   # stored as the default
docsgpt-cli logout support --yes
```

Credentials are picked in this order: `--key <name>` > `DOCSGPT_API_KEY` > the
default key for chatting, `--token` > `DOCSGPT_TOKEN` > the stored token for the
account commands, and `--url` > `DOCSGPT_URL` > the config file for the server.
CI jobs can set the variables instead of logging in.

### Settings

`docsgpt-cli config` opens a settings menu on a terminal. In scripts:

```bash
docsgpt-cli config show                       # settings, keys and token, redacted
docsgpt-cli config set url http://localhost:7091
docsgpt-cli config get auto_update
docsgpt-cli config path                       # ~/.docsgpt/config.json
```

Keys: `url`, `default_key`, `auto_update` (on/notify/off), `banner`
(always/once/never), `theme` (auto/dark/light), `send_current_directory`,
`send_directory_contents`, `send_project_instructions` (AGENTS.md),
`send_last_commands` (off by default), `number_of_last_commands`.

### Local tools

In chats and one-shot answers the agent can run commands, read files and write files on
your machine. Reads run right away; every command and write is shown first
(writes as a short diff) and waits for your answer: **Approve** (`a`),
**Always allow** (`l`), **Deny** (`d`) or **Edit** the command (`e`). Ctrl+C
at the prompt stops the answer.

Always allow lasts until the session ends: for writes it covers every later
write; for a command it covers later commands with the same first word (for
example `git`), as long as they are simple ones: anything with `;`, `&`, `|`,
redirections or substitutions always asks. Approval is the only safeguard,
so read what you approve. `--auto-approve` skips the prompts entirely.

---

## Updating

`docsgpt-cli` keeps itself up to date. It checks GitHub for a new [release](https://github.com/arc53/DocsGPT-cli/releases) in the background at most once a day, downloads and verifies it, and installs it the next time you run a command. Control this behavior with:

```bash
docsgpt-cli config set auto_update on      # download and install automatically (default)
docsgpt-cli config set auto_update notify  # only print a notice when a release is available
docsgpt-cli config set auto_update off     # never check
```

Manual controls:

```bash
docsgpt-cli update            # check, confirm, and install now
docsgpt-cli update --check    # only check for a new version
docsgpt-cli update --yes      # skip the confirmation prompt
docsgpt-cli update --rollback # restore the binary from before the last update
```

A rollback also tells auto-update to skip the version you rolled back from until you run `docsgpt-cli update` yourself.

Setting the `DOCSGPT_NO_UPDATE_CHECK` environment variable disables everything update-related. Homebrew installs are never touched — update those with `brew upgrade --cask docsgpt-cli`. Long-running hosts (`docsgpt-cli host`) check occasionally while idle, install the new release, and restart themselves into it.

## Personal access tokens / CI usage

Agent API keys talk to **one agent**. A **personal access
token** (PAT, `dgpt_pat_…`) acts as **you**, limited to the scopes — and
optionally the specific agents/sources — you granted it. It is what the
account-level commands use, which makes agents and sources deployable from a
terminal or a pipeline. Create one in the DocsGPT web app under
**Settings → Access Tokens**; the CLI consumes a token, it does not create or
revoke them.

```bash
docsgpt-cli login                 # paste the token; or: echo "$TOKEN" | docsgpt-cli login
docsgpt-cli whoami                # active key, plus user, token name, scopes, restrictions
docsgpt-cli logout --token        # forget the stored token (--yes without a terminal)
```

The token is resolved as `--token` flag > `DOCSGPT_TOKEN` > `~/.docsgpt/config.json`
(written with mode `0600`), and the base URL as `--url` > `DOCSGPT_URL` > config,
so CI needs no `login` step at all. The CLI never prints a full token — only its
first characters (`dgpt_pat_AbCdEf…`).

| Command | Scope |
| --- | --- |
| `agents list`, `agents export <id> [-o file]` | `agents:read` |
| `agents plan -f …`, `agents apply -f …`, `agents delete <id> [--yes]` | `agents:write` |
| `agents trigger <id> -f …` (`agents trigger --webhook-url …` needs no token) | `agents:keys` |
| `sources list` | `sources:read` |
| `sources upload <file…> --name N [--wait]`, `sources delete <id> [--yes]` | `sources:write` |
| `agents prompts`, `agents tools` | `prompts:read`, `tools:read` |
| `bench` with `agent_id:` / `--agent-id` | `chat:run` |

A `write` scope includes the matching `read` scope. A token that lacks a scope gets a clear error naming it
(`the token lacks the required scope "agents:write"`); every list command takes
`--json` for the raw server document.

### Agents as code

```bash
docsgpt-cli agents export <id> -o agents/support.agent.yaml   # secrets are never exported
docsgpt-cli agents plan  -f agents/                           # dry run, nothing is written
docsgpt-cli agents apply -f agents/ [--dry-run] [--json]
```

`-f` takes a file, a directory (its `*.yaml`/`*.yml`, sorted) or `-` for stdin
and can be repeated; multi-document files are applied document by document, and
only `kind: Agent` is accepted. An agent is matched by `metadata.id`, then
`metadata.slug`: a match is updated in place (status and API key kept),
anything else is created as a draft.

`apply` always plans first and prints, per document, create vs update and how
each reference (sources, tools, prompt, models) resolves. If anything is
`MISSING` or `UNAVAILABLE`, it exits `1` **without applying anything** until the
reference is settled with `--resolve` (repeatable):

```bash
docsgpt-cli agents apply -f agents/ \
  --resolve "source:Handbook=<source-id>" \
  --resolve "tool:web-search.secret.token=$BRAVE_TOKEN" \
  --resolve "tool:legacy=skip" \
  --resolve "model:My LLM=$LLM_API_KEY"
```

`source:<name>=<source-id>` attaches an existing source (`=skip` accepts that it
stays unattached); `tool:<sel>=reuse:<tool-id>|create|skip` decides a tool and
`tool:<sel>.secret.<field>=<value>` supplies a secret for one that gets created;
`model:<display-name>=<api-key>` creates a custom model (`=skip` drops it).
`<sel>` is the plan key (`tool-0`, …) or the tool's name/type. See
[`examples/agents`](examples/agents) for a sample definition and the full
reference. Exit codes match `bench`: `0` ok, `1` failed or blocked,
`2` usage / validation error.

### Triggering an agent

`agents trigger` posts a JSON payload to an agent's incoming webhook. The whole
payload becomes the agent's input; the agent runs asynchronously and the
command prints the task id. `--wait` polls the run and prints the agent's
answer instead.

```bash
docsgpt-cli agents trigger --webhook-url "$WEBHOOK_URL" -f payload.json   # prints the task id
echo '{"event":"deploy","env":"prod"}' | docsgpt-cli agents trigger <agent-id> -f - --wait
docsgpt-cli agents trigger <agent-id> -f payload.json --wait --json | jq -r .answer
```

Address the agent in one of two ways, not both:

- `--webhook-url <url>` (or `DOCSGPT_WEBHOOK_URL` when neither the flag nor an
  agent id is given): no personal access token is needed, since the URL itself
  is the secret. Copy it from the agent's details in the web app.
- `<agent-id>`: the CLI looks the webhook up with your token (scope
  `agents:keys`), honouring `--url`/`DOCSGPT_URL` and
  `--token`/`DOCSGPT_TOKEN`/the stored token. The server creates the webhook if
  the agent has none yet.

The payload comes from `-f <file>` or `-f -` (stdin) and must be valid JSON; it
is checked before anything is sent. `--idempotency-key <k>` makes retries safe:
a repeat with the same key within about 24 hours returns the original task
instead of running the agent again. `--wait` polls for up to `--timeout`
(default `10m`) and `--json` prints `task_id`, `status`, `answer` and the task's
`result` verbatim (`result.result.tool_calls`, `sources`, `thought`). The
webhook URL is never printed: output and errors show it as
`<base>/api/webhooks/agents/...`.

`--wait` reads `/api/task_status` without credentials, which is how DocsGPT
serves it. If a server requires them anyway, the token is used (it needs
`chat:run`, `sources:read` or `sources:write`), and only when it is configured
for the webhook's host.

Exit codes: `0` ok, `1` the webhook call or the agent run failed or timed out
(including a run the server reports as not successful, such as an exceeded
quota), `2` usage error (no target or both, missing or invalid JSON, a
malformed webhook URL).

A GitHub Actions job that relays an issue or pull request to a triage agent:

```yaml
jobs:
  triage:
    runs-on: ubuntu-latest
    env:
      DOCSGPT_WEBHOOK_URL: ${{ secrets.TRIAGE_WEBHOOK_URL }}
      DOCSGPT_NO_UPDATE_CHECK: "1"
      NUMBER: ${{ github.event.pull_request.number || github.event.issue.number }}
    steps:
      - name: Install docsgpt-cli
        run: |
          curl -fsSL -o docsgpt-cli.tar.gz \
            https://github.com/arc53/DocsGPT-cli/releases/latest/download/docsgpt-cli_linux_amd64.tar.gz
          tar -xzf docsgpt-cli.tar.gz docsgpt-cli && sudo mv docsgpt-cli /usr/local/bin/
      - name: Send the event to the agent
        run: |
          jq -n --arg repo "$GITHUB_REPOSITORY" --arg event "$GITHUB_EVENT_NAME" --argjson number "$NUMBER" \
            '{kind: $event, repo: $repo, number: $number}' > payload.json
          docsgpt-cli agents trigger -f payload.json --idempotency-key "run-$GITHUB_RUN_ID"
```

`GITHUB_RUN_ID` stays the same when a failed job is re-run, so a re-run does not
start the agent a second time, while every new event gets its own key.

### Sources

```bash
docsgpt-cli sources upload docs/*.md --name "Product docs" --wait --replace --timeout 15m
docsgpt-cli sources list
docsgpt-cli sources delete <id> --yes
```

`--wait` polls the ingestion task (progress on stderr) and exits non-zero when
it fails or times out. Uploads are safe to retry: the `Idempotency-Key` defaults
to a hash of `--name` plus the file contents, so re-running the same upload
returns the original task instead of ingesting twice (the server remembers keys
for about a day). Pass `--idempotency-key <k>` to choose your own, or
`--idempotency-key ""` to send none. `delete` refuses to run without `--yes`
when there is no terminal.

**Updating a source from CI: use `--replace`.** Every upload creates a new
source, and the server resolves the source an agent names in its YAML to the
*oldest* source with that name. Without `--replace`, the second push leaves a
second "Product docs" behind and the agent keeps answering from the first one.
`--replace` (needs `--wait`) deletes your older sources with the same name once
the new one is ingested; run `agents apply` afterwards so agents that reference
the source by name are re-pointed at the new one. Team-shared sources are never
deleted. If you revert documentation to content that was uploaded within the
last day, the server deduplicates the request and reports the earlier source,
which may be gone by now; `--replace` then deletes nothing and exits non-zero.
Re-run with a fresh `--idempotency-key` (for example the commit SHA) to ingest
it again.

### GitHub Actions

```yaml
env:
  DOCSGPT_TOKEN: ${{ secrets.DOCSGPT_TOKEN }}   # scopes: agents:write, sources:write, chat:run
  DOCSGPT_URL: ${{ vars.DOCSGPT_URL }}
  DOCSGPT_NO_UPDATE_CHECK: "1"

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Install docsgpt-cli
        run: |
          curl -fsSL -o docsgpt-cli.tar.gz \
            https://github.com/arc53/DocsGPT-cli/releases/latest/download/docsgpt-cli_linux_amd64.tar.gz
          tar -xzf docsgpt-cli.tar.gz docsgpt-cli && sudo mv docsgpt-cli /usr/local/bin/
      - run: docsgpt-cli whoami
      - run: docsgpt-cli sources upload docs/*.md --name "Product docs" --wait --replace --idempotency-key "docs-${{ github.sha }}"
      - run: docsgpt-cli agents apply -f agents/
      - run: docsgpt-cli bench ./bench --target stream --agent-id "${{ vars.DOCSGPT_AGENT_ID }}" --junit bench.xml
```

A fuller workflow (plan on pull requests, deploy on `main`) is in
[`examples/ci/github-actions.yml`](examples/ci/github-actions.yml). Give CI its
own narrowly scoped token, restrict it to the agents/sources it deploys where
possible, and revoke it in the web app if it leaks.

---

## Benchmarking Agents

`docsgpt-cli bench` runs a directory of benchmark cases against your agents and
asserts on the answers — a quick "is everything still good?" check for prompt,
model, or source changes.

```bash
docsgpt-cli bench init my-suite          # scaffold a suite
docsgpt-cli bench                        # run ./bench
docsgpt-cli bench --json                 # machine-readable output
docsgpt-cli bench --junit out.xml        # JUnit XML for CI
docsgpt-cli bench --vs other-key         # A/B compare two agents
docsgpt-cli bench --model gpt-5.6-terra  # pin one model for every case
docsgpt-cli bench --matrix m1,m2,m3      # run once per model, print the comparison table
docsgpt-cli bench --baseline last        # diff against the previous run
docsgpt-cli bench record                 # snapshot answers as golden files
```

Each case is a directory with a `case.yaml` (a question or a multi-turn
`turns:` list, optional attachments, and `expect` assertions on the answer
text, JSON fields, sources, tool calls, LLM-as-judge rubrics, latency and
time-to-first-token, token budgets, SSE integrity, or — for negative cases —
the expected server error). Cases can run through four targets: `v1`
(OpenAI-compatible endpoint, reports token usage; optionally streamed),
`stream` (native SSE), `answer` (native `/api/answer`), or `webhook` (async
agent webhooks). Reports include p50/p95 latency and TTFT, tokens, and an
estimated cost when pricing is known. Exit codes are CI-friendly: `0` pass,
`1` failures, `2` configuration error.

The `stream` and `answer` targets can also run an agent **by id** with your
personal access token (scope `chat:run`) instead of an agent API key: set
`agent_id:` in `bench.yaml` / `case.yaml` (mutually exclusive with `agent:`) or
pass `--agent-id <id>`. The request then carries `agent_id` plus
`Authorization: Bearer <token>`; `v1` and `webhook` keep using the agent API
key / webhook token and reject `agent_id` with a clear error.

The token is only sent to the server you configured (`--url`, else
`DOCSGPT_URL` or the config file). A suite is data, often from someone else's
repository: if its `base_url` points at another origin, `agent_id` cases fail
with an error instead of sending your token there, and pricing for that URL is
fetched anonymously. Pass `--url <that server>` when you do trust it.

See [`examples/bench`](examples/bench) for a ready-made suite.

### Running benchmarks against shared deployments

Nightly runs against a real deployment create real conversations and spend
real tokens, so keep them attributable and easy to exclude:

- Use a **dedicated bench account** and its agent API keys, never a personal
  one. Bench conversations stay `visibility: hidden` (the stream/answer
  default) and spend lands under the bench keys.
- Keep keys out of committed YAML: reference them as `${VAR}` and put the
  values in the suite's `.env` (gitignored) or CI secrets.
- Pass `--run-tag <name>` (or set `run_tag:` in `bench.yaml`): every request
  then carries `X-DocsGPT-Bench-Tag: bench:<name>` and a
  `docsgpt-cli/<version> bench` User-Agent, so server-side telemetry can filter
  bench traffic out of user-facing metrics and error reviews.

## Customizing the Prompt

We recommend changing the default DocsGPT prompt to make your interactions more efficient. By using a more concise prompt, you can get faster and more focused responses. For example, you can set the prompt to:

```
You are a embedded cli assistant docsgpt. You help users from terminal. Keep your answers very short. Just answer with a command if applicable.
```

---

## Code Of Conduct

We as members, contributors, and leaders, pledge to make participation in our community a harassment-free experience for everyone, regardless of age, body size, visible or invisible disability, ethnicity, sex characteristics, gender identity and expression, level of experience, education, socio-economic status, nationality, personal appearance, race, religion, or sexual identity and orientation. Please refer to the [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) file for more information about contributing.
