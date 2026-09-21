#!/usr/bin/env bash
# DocsGPT-cli installer for macOS and Linux.
#
#   curl -fsSL https://docs.ac/install-cli | bash
#
# Downloads the release archive for this platform, checks it against the
# published checksums, and runs `docsgpt-cli install` to place it on PATH.
# Running it again installs the newest release over the old one.
#
# Environment:
#   DOCSGPT_CLI_VERSION     release tag to install (default: the latest release)
#   DOCSGPT_NO_MODIFY_PATH  set to 1 to leave shell profiles alone
#
# Everything runs inside main(), so a download cut short runs nothing.

REPO="arc53/DocsGPT-cli"

# Set once main() has a temp directory, and read by the EXIT trap. This has to
# live at file scope: a variable local to main() is already out of scope by the
# time the trap runs, which under `set -u` would fail the script after it had
# otherwise succeeded.
tmp=""
cleanup() {
  if [ -n "${tmp:-}" ]; then
    rm -rf "$tmp"
  fi
}

main() {
  set -euo pipefail

  local bold="" red="" reset=""
  if [ -t 2 ]; then
    bold=$'\033[1m' red=$'\033[31m' reset=$'\033[0m'
  fi
  say() { printf '%s==>%s %s\n' "$bold" "$reset" "$*" >&2; }
  die() { printf '%serror:%s %s\n' "$red" "$reset" "$*" >&2; exit 1; }
  has() { command -v "$1" >/dev/null 2>&1; }
  download() {
    if has curl; then
      curl -fsSL --retry 3 "$1"
    elif has wget; then
      wget -qO- "$1"
    else
      die "curl or wget is needed to download $1"
    fi
  }
  sha256_of() {
    if has shasum; then
      shasum -a 256 "$1" | awk '{print $1}'
    elif has sha256sum; then
      sha256sum "$1" | awk '{print $1}'
    else
      die "neither shasum nor sha256sum is available to verify the download"
    fi
  }

  local os arch
  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "this installer is for macOS and Linux. On Windows, in PowerShell: irm https://docs.ac/install-cli.ps1 | iex" ;;
  esac

  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "no released binary for $(uname -m). Build from source: https://github.com/$REPO" ;;
  esac

  # Under Rosetta uname reports x86_64 on an arm64 Mac; install the native build.
  if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
    [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
    arch=arm64
  fi

  # An existing Homebrew install would be shadowed by whatever we put on PATH,
  # and `brew upgrade` would then fight this installer over the same command.
  # Caskroom matters as much as Cellar: an Intel Mac's prefix is /usr/local, so
  # a cask there resolves to /usr/local/Caskroom/... with "homebrew" nowhere in
  # the path. `brew --prefix` catches the rest, including a custom prefix.
  local existing
  existing="$(command -v docsgpt-cli 2>/dev/null || true)"
  if [ -n "$existing" ]; then
    # readlink -f is GNU; macOS only gained it in 12.3, so fall back to the
    # unresolved path rather than losing the check on older systems.
    local resolved="$existing" resolved_link=""
    if resolved_link="$(readlink -f "$existing" 2>/dev/null)" && [ -n "$resolved_link" ]; then
      resolved="$resolved_link"
    fi
    local brew_bin=""
    if has brew; then
      brew_bin="$(brew --prefix 2>/dev/null || true)/bin"
    fi
    case "$resolved" in
      */Cellar/* | */Caskroom/* | */homebrew/*)
        die "docsgpt-cli is installed by Homebrew at $existing. Upgrade it with: brew upgrade --cask docsgpt-cli"
        ;;
    esac
    if [ -n "$brew_bin" ] && [ "$brew_bin" != "/bin" ] && [ "$(dirname "$existing")" = "$brew_bin" ]; then
      die "docsgpt-cli is installed by Homebrew at $existing. Upgrade it with: brew upgrade --cask docsgpt-cli"
    fi
  fi

  local base archive
  archive="docsgpt-cli_${os}_${arch}.tar.gz"
  if [ -n "${DOCSGPT_CLI_VERSION:-}" ]; then
    local version="$DOCSGPT_CLI_VERSION"
    case "$version" in v*) ;; *) version="v$version" ;; esac
    base="https://github.com/$REPO/releases/download/$version"
    say "Installing docsgpt-cli $version"
  else
    base="https://github.com/$REPO/releases/latest/download"
    say "Installing the latest docsgpt-cli"
  fi

  trap cleanup EXIT
  tmp="$(mktemp -d)"

  download "$base/$archive" >"$tmp/$archive" ||
    die "could not download $base/$archive"

  # The checksums file is part of the same release, so a partial or swapped
  # archive fails here rather than being unpacked and run.
  local sums expected actual
  sums="$tmp/checksums.txt"
  download "$base/checksums.txt" >"$sums" ||
    die "could not download $base/checksums.txt"
  expected="$(awk -v name="$archive" '$2 == name || $2 == "*" name {print $1; exit}' "$sums")"
  [ -n "$expected" ] || die "$archive is not listed in checksums.txt"
  actual="$(sha256_of "$tmp/$archive")"
  if [ "$actual" != "$expected" ]; then
    die "$archive does not match its published sha256: got $actual, expected $expected. Refusing to install it."
  fi

  tar -xzf "$tmp/$archive" -C "$tmp" || die "could not unpack $archive"
  [ -f "$tmp/docsgpt-cli" ] || die "$archive did not contain a docsgpt-cli binary"
  chmod +x "$tmp/docsgpt-cli"

  # `install` moves the binary out of $tmp and onto PATH, and knows where each
  # platform puts it. DOCSGPT_NO_UPDATE_CHECK keeps the freshly unpacked binary
  # from spawning an update check before it has been installed.
  DOCSGPT_NO_UPDATE_CHECK=1 "$tmp/docsgpt-cli" install ||
    die "docsgpt-cli install failed"

  say "Run 'docsgpt-cli --help' to get started."
}

main "$@"
