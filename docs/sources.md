# Sources

Sources are the documents agents answer from. The `sources` commands need a
[personal access token](agents-as-code.md#personal-access-token): `sources:read`
to list, `sources:write` to upload and delete.

```bash
docsgpt-cli sources list
docsgpt-cli sources upload docs/*.md --name "Product docs" --wait
docsgpt-cli sources delete <id> --yes
```

## Upload

`sources upload <file>…` uploads documents (or `.zip` archives) as a new source
named `--name`. Ingestion runs on the server.

| Flag | Effect |
|---|---|
| `--name <name>` | Name of the new source (required) |
| `--wait` | Poll ingestion (progress on stderr); exit non-zero if it fails or times out |
| `--timeout <d>` | How long `--wait` polls (default `10m`) |
| `--replace` | After ingestion, delete your older sources with the same name (needs `--wait`) |
| `--idempotency-key <k>` | Choose the key; `""` sends none |
| `--json` | Print the result as JSON |

Uploads are safe to retry. The `Idempotency-Key` defaults to a hash of
`--name` and the files' contents, so re-running the same upload (a retried CI
job) returns the original task instead of ingesting twice, while any change to
the name or content gives a new key. The server remembers keys for about a
day; to ingest identical content again within that window, pass your own key.

## Keep a source up to date: `--replace`

Every upload creates a new source, and the server resolves the source an agent
names in its YAML to the **oldest** source with that name. Without `--replace`,
the second upload leaves a second "Product docs" behind and agents keep
answering from the first one.

```bash
docsgpt-cli sources upload docs/*.md --name "Product docs" --wait --replace --timeout 15m
docsgpt-cli agents apply -f agents/
```

`--replace` deletes your older sources with the same name once the new one is
ingested. Run `agents apply` afterwards, so agents that name the source are
re-pointed at the new one.

- Team-shared sources are never deleted, nor is the new one.
- If you revert the docs to content uploaded within the last day, the server
  deduplicates the request and reports the earlier source, which may be gone by
  now. `--replace` then deletes nothing and exits `1`. Re-run with a fresh
  `--idempotency-key` (the commit SHA, say) to ingest it again.

## Delete

`sources delete <id>` deletes a source and its index. It asks first, and
refuses without `--yes` when there is no terminal.

Exit codes: `0` ok, `1` upload or ingestion failed or timed out, `2` usage
error.
