# Releasing

Releases are cut from GitHub, not from a laptop: **Actions → Release → Run
workflow**. Pick a bump for the CLI, the sdk module, or both.

| Input | Values | Meaning |
| --- | --- | --- |
| `cli` | `none`, `patch`, `minor`, `major`, or `1.7.0` | The `docsgpt-cli` release |
| `sdk` | `none`, `patch`, `minor`, `major`, or `0.2.0` | The `github.com/arc53/DocsGPT-cli/sdk` module |

Versions are computed from the newest **release** tag, so a `patch` on `v1.6.0`
becomes `v1.6.1`. Prereleases are skipped when picking that base: git sorts
`v1.7.0-rc1` above `v1.7.0`, and it is not a version to add one to. An explicit
version is taken as given, and may carry a prerelease suffix.

The run refuses a CLI tag that already exists, refuses `none`/`none`, and
refuses to be re-run — re-running a dispatch recomputes versions against the tags the first
attempt created, which mints a further version instead of finishing the failed
one. (The guard allows re-running a release started by a pushed tag, but if the
first attempt already uploaded assets GoReleaser will refuse the duplicates —
delete the release and tag first.)

The pin commit is pushed to the branch the workflow was dispatched from
(normally `main`), so a dispatch has to come from a branch, not a tag.

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

A CLI-only release (`sdk: none`) skips all of that and just tags and releases —
after checking that `go.mod` does not pin an sdk version *older* than the newest
published sdk **release** tag (prereleases are not counted), so a release cannot silently ship against an sdk older
than the one that exists. A pin ahead of the newest release — a prerelease, say
— is fine.

A change that spans both modules — new sdk API that the CLI then uses — is two
runs: release the sdk first, let the pin bump land, then release the CLI. The
CLI on `main` has to build against the *current* pin at all times, which CI
enforces on every PR.

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

## When a release fails halfway

The steps are ordered so the cheapest failures happen first: everything is built
and tested before any tag is pushed, and again after the pin bump but *before*
that commit is pushed.

If a run fails after the sdk tag was pushed but before the pin landed, that tag
is published and nothing references it. Do **not** re-run the job — start a new
one with `sdk:` set to that exact version (e.g. `0.2.0`). The workflow sees the
tag already exists and the pin is behind it, skips tagging, and just pins and
commits it. A CLI-only release in that state is refused, with that remedy in
the message — unless the tag that was published is a prerelease, which the
staleness check does not count.

Asking for an sdk version that is already published and *not* behind the current
pin is an error rather than a silent no-op — otherwise a typo would push a pin
downgrade and build the CLI against an older sdk.

If GoReleaser itself fails after the CLI tag was pushed — an expired
`HOMEBREW_TAP_TOKEN`, say — the tag and possibly a partial release exist. Fix
the cause, delete the release and tag, and run again.

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
