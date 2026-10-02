# Install and update

## Install script (macOS, Linux)

```bash
curl -fsSL https://docs.ac/install-cli | bash
```

On Windows, in PowerShell:

```powershell
irm https://docs.ac/install-cli.ps1 | iex
```

The script downloads the release archive for your platform, checks it against
the published `checksums.txt`, and hands over to `docsgpt-cli install`, which
puts the binary on your `PATH`: `/usr/local/bin` when it is writable, else
`~/.local/bin` (`%USERPROFILE%\bin` on Windows). Run it again to upgrade.

To read the script first, download it: `curl -fsSL https://docs.ac/install-cli -o install.sh`,
then `bash install.sh`.

| Variable | Effect |
|---|---|
| `DOCSGPT_CLI_VERSION` | Install this release instead of the latest (e.g. `v1.5.1`) |
| `DOCSGPT_NO_MODIFY_PATH=1` | Install the binary, leave shell profiles (or the Windows `PATH`) alone |

## Homebrew (macOS)

```bash
brew tap arc53/docsgpt-cli
brew install --cask docsgpt-cli
```

Upgrade with `brew upgrade --cask docsgpt-cli`. Homebrew-managed copies never
update themselves; `docsgpt-cli update` points you back at brew.

Installed before 1.6.0? Those came from a formula, which is no longer updated.
Move across once:

```bash
brew uninstall --formula --force docsgpt-cli && brew install --cask docsgpt-cli
```

Homebrew on Linux is not supported (casks are macOS-only). Use the install
script.

## Release archive

Download the archive for your platform from
[Releases](https://github.com/arc53/DocsGPT-cli/releases). Run the binary as
is, or let it copy itself onto your `PATH`:

```bash
./docsgpt-cli install
```

## go install

```bash
go install github.com/arc53/DocsGPT-cli/cmd/docsgpt-cli@latest
```

Needs v1.6.0 or newer (earlier releases predate the module rename). The binary
lands in `$(go env GOBIN)` (or `$(go env GOPATH)/bin`), knows which release it
came from, and updates itself like any other, so there is no need to re-run
`go install`.

The path ends in `/cmd/docsgpt-cli` on purpose: `go install` names the binary
after the last element of the path, and the module root would give you one
called `DocsGPT-cli`.

## From source

```bash
git clone https://github.com/arc53/DocsGPT-cli
cd DocsGPT-cli
make build
./docsgpt-cli install
```

Source builds report version `dev` and do not update themselves; update them
with `git pull && make build`.

## Updating

`docsgpt-cli` checks GitHub for a new
[release](https://github.com/arc53/DocsGPT-cli/releases) in the background, at
most once a day. It downloads the new binary, verifies its checksum, and swaps
it in the next time you run a command. The previous binary is kept for a
rollback.

```bash
docsgpt-cli config set auto_update on       # download and install (default)
docsgpt-cli config set auto_update notify   # only print a notice
docsgpt-cli config set auto_update off      # never check
```

Manual controls:

```bash
docsgpt-cli update              # check, confirm, install now
docsgpt-cli update --check      # only check
docsgpt-cli update --yes        # no confirmation (needed without a terminal)
docsgpt-cli update --rollback   # restore the binary from before the last update
```

A rollback also tells auto-update to skip the version you rolled back from,
until you run `docsgpt-cli update` yourself.

- `DOCSGPT_NO_UPDATE_CHECK=1` turns off everything update-related (set it in CI).
- Homebrew installs, and binaries in a directory you cannot write to, only get
  a notice; update those with brew or the install script.
- A [host](host.md) checks about every 12 hours while idle, installs the new
  release and restarts itself into it.
- `update` only works on release builds: source builds and prereleases are
  told how to update instead.
