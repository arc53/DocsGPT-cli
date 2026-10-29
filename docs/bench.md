# Bench

`docsgpt-cli bench` runs a directory of cases against your agents and asserts
on the answers: a quick "is everything still good?" after a prompt, model or
source change.

```bash
docsgpt-cli bench init my-suite          # scaffold a suite
docsgpt-cli bench                        # run ./bench
docsgpt-cli bench --json                 # machine-readable output
docsgpt-cli bench --junit out.xml        # JUnit XML for CI
docsgpt-cli bench --vs other-key         # A/B compare two agents
docsgpt-cli bench --model gpt-5.6-terra  # pin one model for every case
docsgpt-cli bench --matrix m1,m2,m3      # run once per model, print a comparison table
docsgpt-cli bench --baseline last        # diff against the previous run
docsgpt-cli bench record                 # snapshot answers as golden files
```

## Suites and cases

A suite is a directory (default `./bench`) with an optional `bench.yaml` of
shared defaults and one subdirectory per case. A case has a `case.yaml`: a
`question` or a multi-turn `turns:` list, optional attachments, and `expect`
assertions on:

- the answer text (contains, regex, …) and JSON fields (gjson paths);
- sources and tool calls;
- an LLM-as-judge rubric (graded by a second agent, `--judge`);
- latency, time to first token and token budgets;
- SSE integrity (`expect.stream`);
- for negative cases, the expected server error (`expect.error`);
- a golden answer (`bench record`).

`repeat:`/`min_pass:` (or `--repeat`, `--min-pass`) tolerate flaky answers.
Filter with `-k <text>` or `--tags a,b`. Runs are saved under
`~/.docsgpt/bench/<suite>/` (`--no-save` skips that) for `--baseline last`.

## Targets

| Target | Endpoint | Notes |
|---|---|---|
| `v1` | `POST /v1/chat/completions` | Reports token usage; `stream: true` for SSE; `attachments_mode: inline` sends files as content parts |
| `stream` | `POST /stream` | Native SSE; records time to first token and frames |
| `answer` | `POST /api/answer` | One JSON reply |
| `webhook` | The agent's webhook | Async, polled to completion; no attachments or turns |

Reports include p50/p95 latency and TTFT, tokens, and an estimated cost when
pricing is known (the server's `/api/models`, or `pricing:` in `bench.yaml`).

### Workflow agents and large frames

A workflow agent's stream also carries a `workflow_run` frame and a
`workflow_step` frame per step. The `stream` target saves the run's id as
`workflow_run_id` in each run (and each turn) of the `--json` report, so a CI
job can read the run's steps, final state and files afterwards with
`GET /api/workflow_runs/<id>` and a token holding `runs:read`.

SSE lines are read whole up to 64 MiB, so a step frame several MiB long is no
problem. A frame past that limit is skipped when the bench doesn't need it
(`workflow_step` and the like): the case goes on, and the run gets a warning,
shown under the case and listed in `warnings` in the JSON report. A frame the
result is built from (`answer`, `structured_answer`, `thought`, `source`,
`tool_calls`, `id`, `error`, `end`) that is too large fails the run instead.
The `v1` target fails on any chunk past the limit, since every chunk carries
part of the answer.

## Agent by id

The `stream` and `answer` targets can run an agent **by id** with your personal
access token (scope `chat:run`) instead of an agent API key: set `agent_id:` in
`bench.yaml` or `case.yaml` (not together with `agent:` at the same level), or
pass `--agent-id <id>`. The request then carries `agent_id` and
`Authorization: Bearer <token>`. `v1` and `webhook` keep using the agent key or
webhook token and refuse `agent_id`.

The token is only sent to the server you configured (`--url`, else
`DOCSGPT_URL` or the config file). A suite is data, often from someone else's
repository: if its `base_url` points elsewhere, `agent_id` cases fail instead
of sending your token there, and pricing for that URL is fetched anonymously.
Pass `--url <that server>` when you trust it.

## Secrets

Any YAML value can reference `${VAR}`, resolved from your shell, the suite's
`.env`, then `./.env` (`$$` escapes a dollar; an unset variable fails the
load). Commit `agent: ${DOCSGPT_BENCH_KEY}` and keep the key in a gitignored
`.env` or CI secrets. `--webhook-url` keeps a webhook token out of YAML.

## Shared deployments

Nightly runs against a real deployment create real conversations and spend
real tokens. Keep them attributable and easy to exclude:

- Use a **dedicated bench account** and its agent keys, never a personal one.
  Bench conversations stay `visibility: hidden` (the stream/answer default) and
  spend lands under the bench keys.
- Keep keys out of committed YAML (see above).
- Pass `--run-tag <name>` (or `run_tag:` in `bench.yaml`): every request then
  carries `X-DocsGPT-Bench-Tag: bench:<name>` and a `docsgpt-cli/<version> bench`
  User-Agent, so server telemetry can filter bench traffic out.

## Exit codes

`0` all passed, `1` failures, `2` configuration error, so `docsgpt-cli bench`
drops straight into [CI](ci.md).

The full case reference, with one example per feature, is in
[`examples/bench`](../examples/bench/README.md).
