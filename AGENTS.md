# Development rules

The architecture and behaviour reference (layout, command contracts, config,
release flow) is `CLAUDE.md`. These are the rules for changing the code.

## Code

- Understand the code you change: read the whole file, and the callers, before
  a non-trivial edit. Do not rely on search snippets.
- `cmd/` declares commands, parses flags and prints. Logic lives in `internal/`
  (and the public chat client in the separate `sdk/` module).
- Keep it simple: fewer concepts, small functions, match the surrounding style.
  Inline single-line helpers that have one call site.
- Delete dead code instead of keeping it around. No backward-compatibility
  shims, aliases or flags unless asked for.
- Comments are short and only where the code is not obvious.
- Errors go to stderr; stdout is the answer or the data (tables, JSON, YAML),
  so it stays pipeable. Exit codes: 0 ok, 1 failure, 2 usage.
- Never print secrets: tokens are redacted (`config.RedactToken`), webhook
  URLs render as `.../api/webhooks/agents/...`.

## Checks

Run before every commit and fix everything they report:

```bash
gofmt -l .                       # must print nothing
go build ./... && go vet ./... && go test ./...
(cd sdk && go test ./...)        # when sdk/ changed
```

- Build binaries outside the repo: `go build -o /tmp/dg ./cmd/docsgpt-cli`.
- Tests and manual runs never touch the real `~/.docsgpt`: use a throwaway
  `HOME`, `httptest` servers or a local mock instead of live APIs.
- Do not edit the `require .../sdk` version in `go.mod`; the release workflow
  bumps it.

## Git

- Several sessions may share the checkout: stage explicit paths
  (`git add <path>`), never `git add -A` / `git add .`, and only commit files
  you changed. Never `git reset --hard`, `git checkout .`, `git clean` or a
  bare `git stash`.
- Small, focused commits with conventional messages:
  `fix(chat): …`, `feat(bench): …`, `refactor(host): …`, `docs: …`,
  `chore: …`.
- Do not push, tag or release; releases are cut from the Release workflow
  (see `RELEASING.md`).
