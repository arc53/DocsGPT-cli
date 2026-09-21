# Releasing

Releases are cut from GitHub, not from a laptop: **Actions → Release → Run
workflow**. Pick a bump for the CLI, the sdk module, or both.

| Input | Values | Meaning |
| --- | --- | --- |
| `cli` | `none`, `patch`, `minor`, `major`, or `1.7.0` | The `docsgpt-cli` release |
| `sdk` | `none`, `patch`, `minor`, `major`, or `0.2.0` | The `github.com/arc53/DocsGPT-cli/sdk` module |

Versions are computed from the newest existing tag, so a `patch` on `v1.6.0`
becomes `v1.6.1`. An explicit version is taken as given. The run refuses a tag
that already exists, and refuses `none`/`none`.

## Why one run does both

The CLI and the sdk are separate modules in this repository, and the CLI pins
the sdk version in `go.mod`. Releasing them means doing this in order:

1. Tag `sdk/vX.Y.Z`.
2. Wait for the module proxy to serve it, `go get` that version, and push the
   `go.mod`/`go.sum` bump to `main`.
3. Tag the CLI **on that commit**, so the released binary is built against the
   sdk version that was just published.

Doing it in the other order ships a CLI built against the previous sdk. The
workflow sequences it, retries the proxy until the new version resolves, and
builds and tests with `GOWORK=off` both before and after the bump — the mode the
release itself uses, since `go.work` would otherwise resolve the working tree
and hide a stale pin.

A CLI-only release (`sdk: none`) skips all of that and just tags and releases.

## What the release produces

1. Binaries for linux/darwin/windows × amd64/arm64 (no windows/arm64), with the
   version stamped via `-X github.com/arc53/DocsGPT-cli/cmd.Version`.
2. Archives under **version-less names** — `docsgpt-cli_<os>_<arch>.tar.gz`
   (`.zip` on Windows) — plus `checksums.txt`. These names are load-bearing: the
   self-updater builds them from `runtime.GOOS`/`GOARCH`
   (`internal/update/apply.go`), and `releases/latest/download/...` URLs depend
   on them. Renaming an asset breaks `docsgpt-cli update` for every installed
   copy.
3. `deployment/install.sh` and `deployment/install.ps1`, attached to the release.
   `docs.ac/install-cli` redirects to `releases/latest/download/install.sh`, so
   whatever ships in a release is what `curl | bash` runs from then on.
4. A Homebrew cask committed to
   [`arc53/homebrew-DocsGPT-cli`](https://github.com/arc53/homebrew-DocsGPT-cli)
   using `HOMEBREW_TAP_TOKEN`.

## After the release

- `brew upgrade --cask docsgpt-cli` picks up the new cask.
- Installed binaries auto-update within a day (see the auto-update flow in
  `CLAUDE.md`); `docsgpt-cli update` does it immediately.
- `curl -fsSL https://docs.ac/install-cli | bash` serves the new version at once.
- `go install github.com/arc53/DocsGPT-cli/cmd/docsgpt-cli@latest` resolves it
  once the proxy has fetched the tag.

## Prerequisites

| Secret | Where | Why |
| --- | --- | --- |
| `HOMEBREW_TAP_TOKEN` | `arc53/DocsGPT-cli` → Settings → Secrets → Actions | Fine-grained token, `contents:write` on `arc53/homebrew-DocsGPT-cli` only. `GITHUB_TOKEN` cannot write to another repository. |

The token expires. When it does, the GitHub release still publishes and only the
cask step fails, so **a green release page does not mean brew was updated** —
check the run, or that a commit landed in the tap.

## Prereleases

Give an explicit version with a suffix, e.g. `1.7.0-rc1`. It is published as a
GitHub **prerelease** (`release.prerelease: auto`), so it does not become
`releases/latest` and the cask is skipped (`skip_upload: auto`). That matters in
three places at once: `install.sh` resolves `releases/latest/download/...`, the
self-updater resolves the same release, and a binary stamped with a prerelease
version fails `IsReleaseVersion` — so it would stop checking for updates
entirely. `IsNewer` also refuses to update to a prerelease, as a second lock.

## Cutting a tag by hand

Pushing a `v*` tag still triggers the same workflow, which skips the version
arithmetic and releases exactly that tag. Nothing else is supported: there is no
local release target, because a laptop release needs push access to the
canonical repository and gets the remote names right, and neither is true of a
fork-based clone.

## Changing an installer

`install.sh` and `install.ps1` are served from the **latest release**, not from
`main`, so a fix only reaches users once a release goes out. Both are linted on
every PR that touches them
([`installer-lint.yml`](.github/workflows/installer-lint.yml)); `install.sh`
also has to keep working on macOS's bash 3.2.
