# Releasing docsgpt-cli

Pushing a `v*` tag is the only manual step. Everything downstream — archives for
five platforms, `checksums.txt`, the installer scripts, and the Homebrew cask —
is produced by GoReleaser from that one tag.

## Cut a release

```bash
make release VERSION=v1.6.0
```

The target refuses to tag unless the version is explicit and well-formed, you
are on `main`, the tree is clean, `HEAD` matches `origin/main`, the tag does not
already exist, and `go test ./...` passes. It then creates an annotated tag and
pushes it, which starts
[`release.yml`](.github/workflows/release.yml).

To tag by hand instead:

```bash
git tag -a v1.6.0 -m v1.6.0 && git push origin v1.6.0
```

## What the release run does

1. Builds `linux`/`darwin`/`windows` × `amd64`/`arm64` (no windows/arm64), with
   the version stamped via `-X docsgpt-cli/cmd.Version`.
2. Publishes archives under **version-less names** —
   `docsgpt-cli_<os>_<arch>.tar.gz` (`.zip` on Windows) — plus `checksums.txt`.
   These names are load-bearing: the self-updater builds them from
   `runtime.GOOS`/`GOARCH` (`internal/update/apply.go`), and
   `releases/latest/download/...` URLs depend on them. Renaming an asset breaks
   `docsgpt-cli update` for every installed copy.
3. Attaches `deployment/install.sh` and `deployment/install.ps1` to the release.
   `docs.ac/install-cli` redirects to `releases/latest/download/install.sh`, so
   whatever ships in a release is what `curl | bash` runs from then on.
4. Commits a Homebrew cask to
   [`arc53/homebrew-DocsGPT-cli`](https://github.com/arc53/homebrew-DocsGPT-cli)
   using `HOMEBREW_TAP_TOKEN`.

The job only runs on `arc53/DocsGPT-cli`; a tag pushed on a fork is ignored so
it cannot overwrite the shared tap with fork URLs.

## After the release

- `brew upgrade docsgpt-cli` picks up the new cask.
- Installed binaries auto-update within a day (see the auto-update flow in
  `CLAUDE.md`); `docsgpt-cli update` does it immediately.
- `curl -fsSL https://docs.ac/install-cli | bash` serves the new version at once.

## Prerequisites

| Secret | Where | Why |
| --- | --- | --- |
| `HOMEBREW_TAP_TOKEN` | `arc53/DocsGPT-cli` → Settings → Secrets → Actions | Fine-grained token, `contents:write` on `arc53/homebrew-DocsGPT-cli` only. `GITHUB_TOKEN` cannot write to another repository. |

The token expires. When it does, the GitHub release still publishes and only the
cask step fails, so **a green release page does not mean brew was updated** —
check the run, or that a commit landed in the tap.

## Prereleases

A tag like `v1.6.0-rc1` publishes archives and installers as usual but skips the
cask (`skip_upload: auto`), so `brew install` keeps serving the last stable
version.

## Changing an installer

`install.sh` and `install.ps1` are served from the **latest release**, not from
`main`, so a fix only reaches users once a release goes out. Both are linted on
every PR that touches them
([`installer-lint.yml`](.github/workflows/installer-lint.yml)); `install.sh`
also has to keep working on macOS's bash 3.2.
