# Example benchmark suite

A minimal suite for `docsgpt-cli bench`. Copy this directory to `./bench` in
your project (the default suite location), point `bench.yaml` at one of your
agents, and run:

```bash
docsgpt-cli bench                 # runs ./bench
docsgpt-cli bench examples/bench  # or point at any suite directory
```

Useful variants:

```bash
docsgpt-cli bench -k json                 # filter cases by name/description
docsgpt-cli bench --tags smoke            # filter by tags
docsgpt-cli bench --repeat 3              # re-run each case, tolerate flakes with min_pass
docsgpt-cli bench --json > run.json       # machine-readable output
docsgpt-cli bench --junit report.xml      # JUnit XML for CI
docsgpt-cli bench --vs other-agent        # A/B compare two agents
docsgpt-cli bench --model gpt-5.6-terra   # pin one model for every case
docsgpt-cli bench --matrix m1,m2,m3       # run once per model, print the comparison table
docsgpt-cli bench --run-tag nightly       # tag requests for server-side telemetry
docsgpt-cli bench --baseline last         # diff against the previous saved run
docsgpt-cli bench record                  # snapshot answers into golden.json files
docsgpt-cli bench init my-suite           # scaffold a fresh suite
```

**Secrets stay out of YAML**: any value can reference environment variables as
`${VAR}` (resolved from your shell, the suite's `.env`, or `./.env` — real env
wins; `$$` escapes a literal dollar; unset variables fail the load). So commit
`agent: ${DOCSGPT_BENCH_KEY}` and keep the key in `.env` (gitignored) or CI
secrets. The webhook token can also be passed with `--webhook-url`.

Each case is a directory with a `case.yaml` (a `question` or a list of
`turns` + `expect` assertions) and optional attachment files. Targets: `v1`
(OpenAI-compatible endpoint, reports token usage; `stream: true` for SSE),
`stream` (native SSE endpoint; records time-to-first-token and frame types),
`answer` (native `/api/answer` JSON endpoint), `webhook` (async agent webhook +
task polling; no attachments, no turns). Run history is stored under
`~/.docsgpt/bench/<suite-name>/`.

Case features worth knowing:

- `model:` (suite/case) or `--model` sends a model id with every request
  (`model_id` on stream/answer, `model` on v1); the effective model is stamped
  into every result. `--matrix a,b,c` runs the suite once per model and prints
  pass rate, mean judge score, p50/p95 latency, TTFT, tokens/case, cost/case.
- `turns:` runs a multi-turn conversation (`conversation_id` carried on
  stream/answer, messages replayed on v1); per-turn `expect` is optional, the
  case-level `expect` grades the last answer.
- `expect.error:` makes a server error the expected outcome (negative cases);
  a successful answer then fails the case.
- `expect.stream:` asserts SSE integrity (`end_frame`, `error_frame`,
  `thought: present|absent`, `frames_contain`) on the stream target;
  `expect.limits.max_first_token_seconds` budgets time to first output.
- `attachments_mode: inline` (v1 only) sends files as base64 content parts
  instead of uploading them first.
- Cost: `usage × price` per run when the server's `/api/models` exposes pricing
  or the suite's `pricing:` map does; the report shows `$` per case and totals.

Cases in this suite: `01-basic-answer`, `02-json-output`, `03-attachment`,
`04-webhook`, `05-multi-turn`, `06-negative-error`, `07-stream-integrity`,
`08-answer-endpoint`.

Exit codes: `0` all passed, `1` failures, `2` configuration error — so
`docsgpt-cli bench` drops straight into CI.
