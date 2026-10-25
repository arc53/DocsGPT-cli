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
| `--sync` | Update your source named `--name` in place, re-embedding only what changed; create it if missing |
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

## Update a source in place: `--sync`

`--replace` re-ingests everything on every upload: the new source is embedded
from scratch, even when one page changed. `--sync` updates your existing source
instead:

```bash
docsgpt-cli sources upload docs.zip --name "Product docs" --wait --sync
```

- The files, and the folders inside `.zip` archives, become the source's whole
  file set. Files that are no longer in the upload are removed from it.
- The server re-embeds only added, edited and removed files. With nothing
  changed, nothing is embedded.
- The source keeps its id, so agents that use it need no `agents apply`.
- With no source of that name yet, `--sync` creates it like a plain upload.
  With several of your sources sharing the name, it exits `1` and lists them:
  delete all but one first.
- Team-shared sources are never synced. `--sync` and `--replace` cannot be
  combined.
- A sync sends no derived `Idempotency-Key`. Repeating one is harmless: the
  same files re-embed nothing. A key derived from the content would make a
  revert within the server's dedup window replay the earlier sync instead of
  writing the files. An explicit `--idempotency-key` is still sent.

`--sync` needs a DocsGPT server whose `manage_source_files` endpoint has the
`sync` operation; an older server answers `400`.

## Delete

`sources delete <id>` deletes a source and its index. It asks first, and
refuses without `--yes` when there is no terminal.

Exit codes: `0` ok, `1` upload or ingestion failed or timed out, `2` usage
error.
