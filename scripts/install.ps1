#Requires -Version 5.1
<#
.SYNOPSIS
    Download, verify, and install guise - the Windows browser-routing shim.

.DESCRIPTION
    Fetches the latest released guise.exe from GitHub, verifies it against the
    release's published SHA-256 (guise.exe.sha256), installs it under
    %LOCALAPPDATA%\Programs\Guise, registers it as an eligible browser
    (guise.exe --register), and launches the tray (guise.exe --tray).

    Everything is written to HKEY_CURRENT_USER, so no admin rights are needed.

    Run it straight from the web:

        irm https://raw.githubusercontent.com/jjshanks/guise/main/scripts/install.ps1 | iex

    Because `iex` cannot pass parameters, overrides come from environment
    variables set before the pipe:

      # Pin a specific release instead of latest:
      $env:GUISE_VERSION='v1.2.3'; irm https://raw.githubusercontent.com/jjshanks/guise/main/scripts/install.ps1 | iex

      # Install somewhere other than %LOCALAPPDATA%\Programs\Guise:
      $env:GUISE_INSTALL_DIR='D:\Apps\Guise'; irm https://raw.githubusercontent.com/jjshanks/guise/main/scripts/install.ps1 | iex

    Run as a local file (e.g. from a clone) and these are real parameters:

        ./scripts/install.ps1 -Version v1.2.3 -InstallDir D:\Apps\Guise
#>
[CmdletBinding()]
param(
    # Release tag to install (e.g. v1.2.3). Default: the latest stable release.
    [string]$Version = $env:GUISE_VERSION,
    # Target install directory. Default: %LOCALAPPDATA%\Programs\Guise.
    [string]$InstallDir = $env:GUISE_INSTALL_DIR
)

$ErrorActionPreference = "Stop"

$Owner = "jjshanks"
$Repo  = "guise"
$ExeName = "guise.exe"
$ShaName = "$ExeName.sha256"

function Write-Step([string]$msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Note([string]$msg) { Write-Host "    $msg" }
function Die([string]$msg) { Write-Host "error: $msg" -ForegroundColor Red; exit 1 }

# --- Guards --------------------------------------------------------------------
# guise is a Windows-only, amd64-only binary. Refuse to "install" anywhere else
# rather than copy a binary that can never run.
$onWindows = ($env:OS -eq "Windows_NT") -or ($PSVersionTable.Platform -eq "Win32NT")
if (-not $onWindows) {
    Die "guise only runs on Windows. (Detected a non-Windows host.)"
}
$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -and $arch -ne "AMD64") {
    Die "guise ships only an amd64 build; this machine reports PROCESSOR_ARCHITECTURE=$arch."
}

# PS 5.1 defaults to SSL3/TLS1.0 for web requests, which GitHub rejects. Add
# TLS 1.2 (leave any newer protocols the host already enabled in place).
try {
    [Net.ServicePointManager]::SecurityProtocol =
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch { }

if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\Guise" }

# --- Resolve the release tag ---------------------------------------------------
# A versioned asset URL (releases/download/<tag>/...) needs the concrete tag, so
# when none was pinned we ask the API for the latest stable release. If that
# fails (offline-ish, rate-limited), fall back to the redirect URL GitHub serves
# at releases/latest/download/<asset>, which needs no API call - we just lose
# the ability to print which version we installed.
$tag = $Version
$baseUrl = $null
if (-not $tag) {
    Write-Step "Resolving the latest release"
    try {
        $headers = @{ "User-Agent" = "guise-install"; "Accept" = "application/vnd.github+json" }
        $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Owner/$Repo/releases/latest" -Headers $headers
        $tag = $rel.tag_name
        Write-Note "Latest is $tag"
    } catch {
        Write-Note "GitHub API lookup failed ($($_.Exception.Message)); using the latest-download redirect."
        $baseUrl = "https://github.com/$Owner/$Repo/releases/latest/download"
    }
}
if (-not $baseUrl) {
    $baseUrl = "https://github.com/$Owner/$Repo/releases/download/$tag"
}

# --- Download ------------------------------------------------------------------
# PID-derived temp dir name: Date/Random are intentionally avoided (the canonical
# build script notes the same PS-5.1 constraints) and PID is unique enough here.
$tmp = Join-Path $env:TEMP ("guise-install-" + $PID)
if (Test-Path $tmp) { Remove-Item $tmp -Recurse -Force }
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
    $exePath = Join-Path $tmp $ExeName
    $shaPath = Join-Path $tmp $ShaName

    Write-Step "Downloading $ExeName"
    Invoke-WebRequest -Uri "$baseUrl/$ExeName" -OutFile $exePath -UseBasicParsing
    Write-Step "Downloading $ShaName"
    Invoke-WebRequest -Uri "$baseUrl/$ShaName" -OutFile $shaPath -UseBasicParsing

    # --- Verify ----------------------------------------------------------------
    # The checksum file is ASCII "<64-hex>  guise.exe"; the first whitespace
    # field is the hash. A mismatch means a corrupted or tampered download - stop
    # before anything touches the install dir.
    Write-Step "Verifying SHA-256"
    $expected = ((Get-Content $shaPath -Raw).Trim() -split '\s+')[0].ToLower()
    if ($expected -notmatch '^[0-9a-f]{64}$') {
        Die "checksum file did not contain a valid SHA-256: '$expected'"
    }
    $actual = (Get-FileHash $exePath -Algorithm SHA256).Hash.ToLower()
    if ($actual -ne $expected) {
        Die "checksum mismatch - refusing to install.`n  expected $expected`n  actual   $actual"
    }
    Write-Note "OK ($actual)"

    # --- Stop a running tray so the locked exe can be replaced -----------------
    # ROUTE invocations are short-lived; only the resident --tray process holds a
    # handle on guise.exe. Stop it (and clear any .old/.new left by a prior
    # in-place auto-update) before overwriting.
    $running = Get-Process -Name "guise" -ErrorAction SilentlyContinue
    if ($running) {
        Write-Step "Stopping the running guise tray"
        $running | Stop-Process -Force
        Start-Sleep -Milliseconds 500
    }

    # --- Install ---------------------------------------------------------------
    Write-Step "Installing to $InstallDir"
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $dest = Join-Path $InstallDir $ExeName
    foreach ($leftover in @("$dest.old", "$dest.new")) {
        if (Test-Path $leftover) { Remove-Item $leftover -Force -ErrorAction SilentlyContinue }
    }
    Copy-Item $exePath $dest -Force

    # --- Register + launch -----------------------------------------------------
    Write-Step "Registering guise as a browser"
    & $dest --register
    if ($LASTEXITCODE -ne 0) { Die "guise.exe --register exited with code $LASTEXITCODE" }

    Write-Step "Launching the tray"
    Start-Process -FilePath $dest -ArgumentList "--tray"
}
finally {
    Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

# --- Next steps ----------------------------------------------------------------
$ver = if ($tag) { " $tag" } else { "" }
Write-Host ""
Write-Host "guise$ver installed to $InstallDir" -ForegroundColor Green
Write-Host ""
Write-Host "Two manual steps remain (Windows 11 forbids automating them):" -ForegroundColor Yellow
Write-Host "  1. Set guise as your default browser:"
Write-Host "       Settings -> Apps -> Default apps -> Guise -> Set default"
Write-Host "     (the tray's 'Default browser: No - click to fix' item deep-links there)"
Write-Host "  2. Toggle 'Start at login' in the tray menu to autostart guise."
Write-Host ""
Write-Host "Edit routing rules from the tray ('Edit rules...'). See"
Write-Host "  https://github.com/$Owner/$Repo for documentation."
