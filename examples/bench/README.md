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
docsgpt-cli bench --baseline last         # diff against the previous saved run
docsgpt-cli bench record                  # snapshot answers into golden.json files
docsgpt-cli bench init my-suite           # scaffold a fresh suite
```

**Secrets stay out of YAML**: any value can reference environment variables as
`${VAR}` (resolved from your shell, the suite's `.env`, or `./.env` — real env
wins; `$$` escapes a literal dollar; unset variables fail the load). So commit
`agent: ${DOCSGPT_BENCH_KEY}` and keep the key in `.env` (gitignored) or CI
secrets. The webhook token can also be passed with `--webhook-url`.

Each case is a directory with a `case.yaml` (question + `expect` assertions)
and optional attachment files. Targets: `v1` (OpenAI-compatible endpoint,
reports token usage), `stream` (native SSE endpoint), `webhook` (async agent
webhook + task polling; no attachments). Run history is stored under
`~/.docsgpt/bench/<suite-name>/`.

Exit codes: `0` all passed, `1` failures, `2` configuration error — so
`docsgpt-cli bench` drops straight into CI.
