# DocsGPT-CLI

DocsGPT-cli is a command-line interface (CLI) tool that allows you to interact with [DocsGPT](https://github.com/arc53/DocsGPT). It enables you to ask questions, configure settings, and manage DocsGPT API keys directly from your terminal.

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
> brew uninstall docsgpt-cli && brew install --cask docsgpt-cli
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

### 4. Compile from Source

If you want to make adjustments or compile the binary yourself, clone the repository and compile it:

```bash
git clone https://github.com/arc53/docsgpt-cli.git
cd docsgpt-cli
make build
```

After compiling, follow the same steps as for the binary.

---

## Usage

Once installed, you can start using `docsgpt-cli` by running the following commands:

```bash
docsgpt-cli [flags]
docsgpt-cli [command]
```

### Available Commands:

- `agents` — List, export, plan, apply and delete agents (personal access token)
- `ask` — Ask a question to DocsGPT
- `bench` — Run benchmark suites against your agents (see below)
- `chat` — Start an interactive chat session
- `config` — Manage CLI configuration (base URL, theme, banner, update check)
- `help` — Help about any command
- `install` — Install docsgpt-cli to your system's `PATH`
- `keys` — Manage DocsGPT API keys (add, set default, delete)
- `login` / `logout` / `whoami` — Store, remove and inspect a personal access token
- `prompts` / `tools` — List prompts and configured tools (personal access token)
- `sources` — List, upload and delete sources (personal access token)
- `update` — Update docsgpt-cli to the latest release

### Flags:

- `-h, --help` — Help for docsgpt-cli
- `-v, --version` — Version for docsgpt-cli
- `--url` — Override the API base URL (else `DOCSGPT_URL`, else the config file)
- `--token` — Personal access token (else `DOCSGPT_TOKEN`, else the stored token)

You can use `docsgpt-cli [command] --help` to get more information about each command.

---

## Updating

`docsgpt-cli` keeps itself up to date. It checks GitHub for a new [release](https://github.com/arc53/DocsGPT-cli/releases) in the background at most once a day, downloads and verifies it, and installs it the next time you run a command. Control this behavior with:

```bash
docsgpt-cli config set-auto-update on      # download and install automatically (default)
docsgpt-cli config set-auto-update notify  # only print a notice when a release is available
docsgpt-cli config set-auto-update off     # never check
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

Agent API keys (`docsgpt-cli keys`) talk to **one agent**. A **personal access
token** (PAT, `dgpt_pat_…`) acts as **you**, limited to the scopes — and
optionally the specific agents/sources — you granted it. It is what the
account-level commands use, which makes agents and sources deployable from a
terminal or a pipeline. Create one in the DocsGPT web app under
**Settings → Access Tokens**; the CLI consumes a token, it does not create or
revoke them.

```bash
docsgpt-cli login                 # hidden prompt; or: echo "$TOKEN" | docsgpt-cli login
docsgpt-cli whoami                # user, token name, scopes, resource restrictions
docsgpt-cli logout                # forget the stored token
```

The token is resolved as `--token` flag > `DOCSGPT_TOKEN` > `~/.docsgpt/config.json`
(written with mode `0600`), and the base URL as `--url` > `DOCSGPT_URL` > config,
so CI needs no `login` step at all. The CLI never prints a full token — only its
first characters (`dgpt_pat_AbCdEf…`).

| Command | Scope |
| --- | --- |
| `agents list`, `agents export <id> [-o file]` | `agents:read` |
| `agents plan -f …`, `agents apply -f …`, `agents delete <id> [--yes]` | `agents:write` |
| `sources list` | `sources:read` |
| `sources upload <file…> --name N [--wait]`, `sources delete <id> [--yes]` | `sources:write` |
| `prompts list`, `tools list` | `prompts:read`, `tools:read` |
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
