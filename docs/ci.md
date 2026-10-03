# CI

`docsgpt-cli` needs no login step in CI: set the credentials as environment
variables, and turn off update checks.

| Variable | For |
|---|---|
| `DOCSGPT_TOKEN` | `agents`, `sources`, bench `--agent-id` |
| `DOCSGPT_URL` | Your server (default `https://gptcloud.arc53.com`) |
| `DOCSGPT_API_KEY` | One-shot questions with an agent key |
| `DOCSGPT_WEBHOOK_URL` | `agents trigger` without an agent id |
| `DOCSGPT_NO_UPDATE_CHECK=1` | No update checks |

Give CI its own narrowly scoped [token](agents-as-code.md#personal-access-token),
restrict it to the agents and sources it deploys where possible, and revoke it
in the web app if it leaks.

Exit codes are the same everywhere: `0` ok, `1` failure, `2` usage or
configuration error.

## Deploy sources and agents, then test them

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

- `--replace` keeps one copy of the source; `agents apply` re-points agents at
  it. See [Sources](sources.md#keep-a-source-up-to-date---replace).
- `--junit` gives the CI a test report; see [Bench](bench.md).

A fuller workflow (plan on pull requests, deploy on `main`) is in
[`examples/ci/github-actions.yml`](../examples/ci/github-actions.yml).

## Trigger an agent from an event

A job that relays an issue or pull request to a triage agent through its
[webhook](agents-as-code.md#trigger-an-agent):

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

## Ask once in a pipeline

```bash
git diff origin/main... | DOCSGPT_API_KEY="$REVIEW_KEY" docsgpt-cli "review this diff" > review.md
```

```bash
docsgpt-cli --no-stdin "what changed in the 2.0 API?" > notes.md
```

Off a terminal, only the answer goes to stdout. Stdin is read to its end
unless it is a terminal or `/dev/null`, so a step that pipes nothing in should
pass `--no-stdin` (or `< /dev/null`): some runners keep stdin open, and the
command would wait for it. Tools are not offered unless you pass
`--auto-approve`; only do that in a sandboxed runner (see
[Tools](tools.md#security)).

A request that fails before its answer starts (429, 502, 503, 504, a refused
or dropped connection, a timeout) is retried up to 3 times, after 2s, 4s and
8s, or after the server's `Retry-After` when it gives one (up to a minute).
An answer that breaks off mid-way is not retried. `docsgpt-cli config set retry
off` turns retries off. Errors are one line saying what to do;
`DOCSGPT_DEBUG=1` adds the error as the server or the network gave it.

### JSON output

`--json` writes one JSON object to stdout and nothing else: no header,
rendering or clipboard.

```bash
docsgpt-cli --json --no-stdin "what changed in the 2.0 API?" > answer.json
jq -r .answer answer.json
jq -r '.sources[].url' answer.json
```

```json
{
  "answer": "The 2.0 API …",
  "sources": [{"title": "Changelog", "url": "https://docs.example.com/changelog", "filename": "changelog.md"}],
  "conversation_id": "45f38376-80e8-42b1-a2f3-da84320a800d",
  "model": "gpt-5.1",
  "usage": {"prompt_tokens": 2220, "completion_tokens": 30, "total_tokens": 2250},
  "tool_calls": [{"name": "read_file", "arguments": {"path": "go.mod"}, "result": "module …", "approved": true}]
}
```

- `answer` joins the text of every round of a run with tool calls.
- `sources` have the fields the server sent: `title`, `url` (a URL or a path)
  and `filename`. `usage` is summed over the run, `null` when the server
  reported none; `model` is the model that answered.
- `tool_calls` lists the calls the agent made, with `approved: false` for one
  that was denied or not run.
- On failure the object also has `error` (the same one-line message as on a
  terminal), the fields hold what arrived before it, and the exit code is `1`
  (`2` for a usage error such as no question, `130` when interrupted).
