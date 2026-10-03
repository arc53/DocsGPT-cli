# Agents as code

Keep agent definitions as YAML in a repository, review a plan, and apply it
from a terminal or CI.

```bash
docsgpt-cli agents export <id> -o agents/support.agent.yaml   # secrets are never exported
docsgpt-cli agents plan  -f agents/                           # dry run, nothing is written
docsgpt-cli agents apply -f agents/
```

## Personal access token

Agent API keys talk to one agent. The `agents` and `sources` commands act as
**you**, with a personal access token (`dgpt_pat_…`), limited to the scopes
(and optionally the agents and sources) you granted it. Create one in the
DocsGPT web app under **Settings → Access tokens**; the CLI uses tokens, it
does not create or revoke them.

```bash
docsgpt-cli login           # paste the token; or: echo "$TOKEN" | docsgpt-cli login
docsgpt-cli whoami          # user, token name, scopes, restrictions
docsgpt-cli logout --token  # forget it (--yes without a terminal)
```

In CI, set `DOCSGPT_TOKEN` (and `DOCSGPT_URL`) instead; no `login` step is
needed. See [Configuration](configuration.md#credentials).

| Command | Scope |
|---|---|
| `agents list`, `agents export <id> [-o file]` | `agents:read` |
| `agents plan -f …`, `agents apply -f …`, `agents delete <id> [--yes]` | `agents:write` |
| `agents trigger <id> -f …` (`--webhook-url` needs no token) | `agents:keys` |
| `agents prompts`, `agents tools` | `prompts:read`, `tools:read` |
| `sources list` | `sources:read` |
| `sources upload …`, `sources delete <id> [--yes]` | `sources:write` |
| `bench` with `agent_id:` / `--agent-id` | `chat:run` |

A `write` scope includes the matching `read` scope. A token without a scope
gets an error naming it (`the token lacks the required scope "agents:write"`).
Every list command takes `--json` for the server's raw document.

## Plan and apply

`-f` takes a file, a directory (its `*.yaml`/`*.yml`, sorted, not recursive) or
`-` for stdin, and can be repeated. Multi-document files are applied document
by document; only `kind: Agent` is accepted. An agent is matched by
`metadata.id`, then `metadata.slug`: a match is updated in place (status and
API key kept), anything else is created as a draft.

`apply` always plans every document first and prints, per document, create or
update and how each reference (sources, tools, prompt, models) resolves. If any
is `MISSING` or `UNAVAILABLE`, it exits `1` **without applying anything** until
the reference is settled with `--resolve` (repeatable):

```bash
docsgpt-cli agents apply -f agents/ \
  --resolve "source:Handbook=<source-id>" \
  --resolve "tool:web-search.secret.token=$BRAVE_TOKEN" \
  --resolve "tool:legacy=skip" \
  --resolve "model:My LLM=$LLM_API_KEY"
```

| `--resolve` | Effect |
|---|---|
| `source:<name>=<source-id>` | Attach this existing source |
| `source:<name>=skip` | Accept that the source stays unattached |
| `tool:<sel>=reuse:<tool-id>` | Link one of your existing tools |
| `tool:<sel>=create` | Create a new tool, even if a match exists |
| `tool:<sel>=skip` | Leave the tool off the agent |
| `tool:<sel>.secret.<field>=<value>` | Secret for a tool that gets created |
| `model:<display-name>=<api-key>` | Create a custom model with this key |
| `model:<display-name>=skip` | Accept that the model is dropped |

`<sel>` is the plan key (`tool-0`, `tool-1`, …, the position in `spec.tools`)
or, when unambiguous, the tool's name or type. Positional keys are refused when
several documents are applied at once; an entry that matches nothing is a
usage error.

`plan` is `apply --dry-run`. `--json` prints the plan and results on stdout
(the readable plan goes to stderr). Apply stops at the first document that
fails.

See [`examples/agents`](../examples/agents) for a sample definition.

## Trigger an agent

`agents trigger` posts a JSON payload to an agent's incoming webhook. The whole
payload becomes the agent's input; the agent runs asynchronously and the
command prints the task id. `--wait` waits for the run and prints the answer.

```bash
docsgpt-cli agents trigger --webhook-url "$WEBHOOK_URL" -f payload.json
echo '{"event":"deploy","env":"prod"}' | docsgpt-cli agents trigger <agent-id> -f - --wait
docsgpt-cli agents trigger <agent-id> -f payload.json --wait --json | jq -r .answer
```

Name the agent one way, not both:

- `--webhook-url <url>` (or `DOCSGPT_WEBHOOK_URL`, when neither the flag nor an
  agent id is given): no token needed, the URL itself is the secret. Copy it
  from the agent's details in the web app.
- `<agent-id>`: the CLI looks the webhook up with your token (scope
  `agents:keys`), honouring `--url`/`DOCSGPT_URL` and
  `--token`/`DOCSGPT_TOKEN`/the stored token. The server creates the webhook if
  the agent has none yet.

| Flag | Effect |
|---|---|
| `-f <file>`, `-f -` | The payload; must be valid JSON, checked before anything is sent |
| `--idempotency-key <k>` | A repeat with the same key within about 24 hours returns the original task |
| `--wait` | Poll the run, print the answer, exit non-zero if it fails |
| `--timeout <d>` | How long `--wait` polls (default `10m`) |
| `--json` | Print `task_id`, `status`, `answer` and the task's `result` verbatim (`result.result.tool_calls`, `sources`, `thought`) |

The webhook URL is never printed; output and errors show it as
`<base>/api/webhooks/agents/...`.

`--wait` reads `/api/task_status` without credentials, which is how DocsGPT
serves it. If a server requires them anyway, the token is used (it needs
`chat:run`, `sources:read` or `sources:write`), and only when it is configured
for the webhook's host.

For a GitHub Actions job that relays issues to an agent, see
[CI](ci.md#trigger-an-agent-from-an-event).

## Exit codes

| Code | Meaning |
|---|---|
| `0` | OK |
| `1` | Failed, blocked by an unresolved reference, or timed out (including a run the server reports as not successful, such as an exceeded quota) |
| `2` | Usage or validation error (a malformed YAML, no target or two, invalid JSON, a malformed webhook URL) |

Destructive commands (`agents delete`, `sources delete`) ask first, and refuse
without `--yes` when there is no terminal.
