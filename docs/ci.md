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

Off a terminal, only the answer goes to stdout. Tools are not offered unless
you pass `--auto-approve`; only do that in a sandboxed runner (see
[Tools](tools.md#security)).
