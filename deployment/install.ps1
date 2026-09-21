# DocsGPT-cli installer for Windows.
#
#   irm https://docs.ac/install-cli.ps1 | iex
#
# Downloads the release archive for this machine, checks it against the
# published checksums, and runs `docsgpt-cli install` to place it on PATH.
# Running it again installs the newest release over the old one.
#
# Environment:
#   DOCSGPT_CLI_VERSION     release tag to install (default: the latest release)
#   DOCSGPT_NO_MODIFY_PATH  set to 1 to leave PATH alone
#
# Everything runs inside a function, so a download cut short runs nothing, and
# nothing calls `exit`, which would close the window `iex` runs in.

function Install-DocsGPTCli {
    $ErrorActionPreference = 'Stop'
    $Repo = 'arc53/DocsGPT-cli'

    function Say([string]$Message) { Write-Host "==> $Message" }

    # Windows PowerShell 5.1 on older .NET defaults to TLS 1.0/1.1, which
    # github.com refuses; without this the downloads below fail outright.
    # Only for 5.1: ServicePointManager is static and this script runs inside
    # the caller's session via `iex`, and anywhere the default is already
    # SystemDefault (PowerShell 7, or 5.1 on .NET 4.7+) -bor would pin their
    # whole session to TLS 1.2 for everything else they do afterwards.
    # Wholly inside the try: on .NET < 4.7 SecurityProtocolType has no
    # SystemDefault member, and a caller whose session has Set-StrictMode -Version 2
    # would have the reference itself throw before we could pin anything.
    try {
        if ($PSVersionTable.PSVersion.Major -lt 6 -and
            [Net.ServicePointManager]::SecurityProtocol -ne [Net.SecurityProtocolType]::SystemDefault) {
            [Net.ServicePointManager]::SecurityProtocol =
            [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        }
    }
    catch {}

    # Invoke-WebRequest's progress bar costs more than the transfer on a 17 MB
    # file in Windows PowerShell.
    $previousProgress = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'

    # Only amd64 is released for Windows; Windows on ARM runs it emulated.
    $arch = 'amd64'
    if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') {
        Say 'No native arm64 build for Windows yet; installing the amd64 build, which Windows emulates.'
    }
    $archive = "docsgpt-cli_windows_$arch.zip"

    if ($env:DOCSGPT_CLI_VERSION) {
        $version = $env:DOCSGPT_CLI_VERSION
        if (-not $version.StartsWith('v')) { $version = "v$version" }
        $base = "https://github.com/$Repo/releases/download/$version"
        Say "Installing docsgpt-cli $version"
    }
    else {
        $base = "https://github.com/$Repo/releases/latest/download"
        Say 'Installing the latest docsgpt-cli'
    }

    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("docsgpt-cli-" + [System.Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null
    try {
        $archivePath = Join-Path $tmp $archive
        $sumsPath = Join-Path $tmp 'checksums.txt'
        Invoke-WebRequest -Uri "$base/$archive" -OutFile $archivePath -UseBasicParsing
        Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sumsPath -UseBasicParsing

        # The checksums file is part of the same release, so a partial or
        # swapped archive fails here rather than being unpacked and run.
        $expected = $null
        foreach ($line in Get-Content $sumsPath) {
            $fields = $line -split '\s+', 2
            if ($fields.Count -eq 2 -and $fields[1].Trim().TrimStart('*') -eq $archive) {
                $expected = $fields[0].Trim()
                break
            }
        }
        if (-not $expected) { throw "$archive is not listed in checksums.txt" }
        $actual = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash
        if ($actual -ne $expected.ToUpperInvariant()) {
            throw "$archive does not match its published sha256: got $actual, expected $expected. Refusing to install it."
        }

        Expand-Archive -Path $archivePath -DestinationPath $tmp -Force
        $binary = Join-Path $tmp 'docsgpt-cli.exe'
        if (-not (Test-Path $binary)) { throw "$archive did not contain docsgpt-cli.exe" }

        # `install` moves the binary out of $tmp and onto PATH, and knows where
        # each platform puts it. DOCSGPT_NO_UPDATE_CHECK keeps the freshly
        # unpacked binary from spawning an update check before it is installed.
        $previous = $env:DOCSGPT_NO_UPDATE_CHECK
        $env:DOCSGPT_NO_UPDATE_CHECK = '1'
        try {
            & $binary install
            if ($LASTEXITCODE -ne 0) { throw "docsgpt-cli install failed with exit code $LASTEXITCODE" }
        }
        finally {
            $env:DOCSGPT_NO_UPDATE_CHECK = $previous
        }

        Say "Run 'docsgpt-cli --help' to get started."
    }
    finally {
        $ProgressPreference = $previousProgress
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }
}

Install-DocsGPTCli
