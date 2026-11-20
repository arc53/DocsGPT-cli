# Sources

Sources are the documents agents answer from. The `sources` commands need a
[personal access token](agents-as-code.md#personal-access-token): `sources:read`
to list, `sources:write` to upload and delete.

```bash
docsgpt-cli sources list
docsgpt-cli sources upload docs/*.md --name "Product docs" --wait
docsgpt-cli sources upload ./docs --name "Product docs" --sync --wait
docsgpt-cli sources delete <id> --yes
```

## Upload

`sources upload <file|folder>…` uploads documents, `.zip` archives or whole
folders as a source named `--name`. Ingestion runs on the server.

```bash
docsgpt-cli sources upload docs/*.md --name "Product docs" --wait
docsgpt-cli sources upload ./docs --name "Product docs" --sync --wait
```

| Flag | Effect |
|---|---|
| `--name <name>` | Name of the source (required) |
| `--wait` | Poll ingestion (progress on stderr); exit non-zero if it fails or times out |
| `--timeout <d>` | How long `--wait` polls (default `10m`) |
| `--sync` | Update your source named `--name` in place: upload and re-embed only what changed; create it if missing |
| `--force` | With `--sync`: ask the server even when nothing changed since the last sync |
| `--exclude <glob>` | Leave out a folder's files or folders matching the pattern (repeatable) |
| `--include <glob>` | Upload only a folder's files matching the pattern (repeatable) |
| `--replace` | After ingestion, delete your older sources with the same name (needs `--wait`) |
| `--idempotency-key <k>` | Choose the key; `""` sends none |
| `-q`, `--quiet` | No progress lines, only the result and errors |
| `--json` | Print the result as JSON |

Uploads are safe to retry. The `Idempotency-Key` defaults to a hash of
`--name` and the files' names and contents, so re-running the same upload (a
retried CI job) returns the original task instead of ingesting twice, while
any change to the name or content gives a new key. The server remembers keys
for about a day; to ingest identical content again within that window, pass
your own key.

### Folders

A folder is uploaded with its tree: each file is stored under its path inside
the folder, so `./docs/guide/intro.md` becomes `guide/intro.md` in the source.
Names in any script are fine.

Only files the server can parse are sent: `.md`, `.mdx`, `.rst`, `.txt`,
`.pdf`, `.html`, `.json`, `.csv`, Office and OpenDocument files, `.epub`,
`.rtf`, `.png`/`.jpg` and audio. These are left out, and listed on stderr
(except the hidden ones):

- hidden files and folders (`.git`, `.env`, …)
- other file types, and empty files
- folders reached through a symbolic link inside the folder (a linked file is
  followed, and so is a folder argument that is itself a link)
- whatever `--exclude` matches, and with `--include` whatever it does not

A pattern is a glob (`*`, `?`, `[a-z]`) tried against the path inside the
folder and against each folder and file name in it:

```bash
docsgpt-cli sources upload ./docs --name "Docs" --sync --wait \
  --exclude drafts --exclude node_modules --exclude '*.png'
```

Several folders can be given; their contents are merged, and two files that
would land on the same path are an error. A folder and a `.zip` archive
cannot be combined.

## Keep a source up to date: `--sync`

`--sync` makes your source named `--name` hold exactly the files you pass,
and keeps its id, so agents that use it need no `agents apply`:

```bash
docsgpt-cli sources upload ./docs --name "Product docs" --sync --wait
```

- The CLI sends the list of files with their hashes; the server answers which
  ones it lacks, and only those are uploaded. Files no longer in the folder
  are removed from the source.
- The server re-embeds only added, edited and removed files.
- When nothing changed since the last sync, nothing is sent at all (see
  below).
- A large folder goes in several requests (up to 500 files or 200 MB each), so
  its size is not limited by one upload. Creating a source that large needs
  `--wait`. Any other upload (no `--sync`, `.zip` archives, an older server)
  is one request, within the server's own limits.
- With no source of that name yet, `--sync` creates it. With several of your
  sources sharing the name, it exits `1` and lists them: delete all but one
  first.
- Team-shared sources are never synced. `--sync` and `--replace` cannot be
  combined.
- A sync sends no derived `Idempotency-Key`: repeating one is harmless. An
  explicit `--idempotency-key` is still sent; a key the server has seen within
  a day replays that earlier sync, and no file is compared or sent.

`.zip` archives, and file arguments that share a name, are always sent whole:
only the server can unpack an archive. An older DocsGPT server also takes a
sync only as a whole; the CLI notices and sends every file in one request (the
server still re-embeds only what changed).

### On a schedule

```cron
*/15 * * * *  docsgpt-cli sources upload /srv/docs --name "Docs" --sync --wait --quiet
```

Set `DOCSGPT_TOKEN` (and `DOCSGPT_URL`) in the job's environment, or run
`docsgpt-cli login` once as the user the job runs as.

After a sync the server confirmed (it reported the source unchanged, or
`--wait` saw the ingestion succeed), the CLI records what it sent in
`~/.docsgpt/sync/` (per server, token and source name). While the files stay
the same, the next runs stop there:
no request, exit `0`, `ok source <id> (unchanged, N files)`. Once a day, and
with `--force`, the server is asked anyway, so a source changed or deleted
there is noticed. Without `--wait` nothing is recorded until a later run
hears from the server that the source is up to date.

Two runs at once are safe: the server applies one change to a source's files
at a time, and the CLI waits its turn.

## Replace a source: `--replace`

Without `--sync`, every upload creates a new source, and the server resolves
the source an agent names in its YAML to the **oldest** source with that name.
`--replace` deletes your older sources with the same name once the new one is
ingested:

```bash
docsgpt-cli sources upload docs/*.md --name "Product docs" --wait --replace --timeout 15m
docsgpt-cli agents apply -f agents/
```

Run `agents apply` afterwards, so agents that name the source are re-pointed
at the new one. Everything is uploaded and embedded again each time; prefer
`--sync`.

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
