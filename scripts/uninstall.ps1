#Requires -Version 5.1
<#
.SYNOPSIS
    Remove guise: unregister the browser entries, drop autostart, delete the binary.

.DESCRIPTION
    The mirror of install.ps1. It runs `guise.exe --unregister` to remove the
    HKCU browser-eligibility keys, removes the login autostart Run value (which
    --unregister deliberately does not touch), stops any running tray, and
    deletes the install directory. Everything is HKCU, so no admin rights needed.

    Your rules and log under %APPDATA%\Guise are LEFT IN PLACE so a reinstall
    keeps your config; this script prints how to remove them if you want a clean
    wipe.

    Run it straight from the web:

        irm https://raw.githubusercontent.com/jjshanks/guise/main/scripts/uninstall.ps1 | iex

    Override the install location (matching install.ps1) before the pipe:

        $env:GUISE_INSTALL_DIR='D:\Apps\Guise'; irm https://raw.githubusercontent.com/jjshanks/guise/main/scripts/uninstall.ps1 | iex
#>
[CmdletBinding()]
param(
    # Directory guise was installed to. Default: %LOCALAPPDATA%\Programs\Guise.
    [string]$InstallDir = $env:GUISE_INSTALL_DIR
)

$ErrorActionPreference = "Stop"

function Write-Step([string]$msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Note([string]$msg) { Write-Host "    $msg" }

if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\Guise" }
$exe = Join-Path $InstallDir "guise.exe"

# Unregister the browser entries while the binary still exists to do it cleanly.
if (Test-Path $exe) {
    Write-Step "Unregistering guise as a browser"
    & $exe --unregister
    if ($LASTEXITCODE -ne 0) {
        Write-Note "guise.exe --unregister exited with code $LASTEXITCODE; continuing."
    }
} else {
    Write-Note "No guise.exe at $exe; skipping --unregister."
}

# Stop the resident tray so the binary isn't locked when we delete it.
$running = Get-Process -Name "guise" -ErrorAction SilentlyContinue
if ($running) {
    Write-Step "Stopping the running guise tray"
    $running | Stop-Process -Force
    Start-Sleep -Milliseconds 500
}

# Remove the login autostart Run value. --unregister does not touch this (it is
# guise's own toggle, value name "Guise" under the HKCU Run key), so clear it
# here for a complete uninstall. Missing value is fine.
Write-Step "Removing login autostart"
Remove-ItemProperty -Path "HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run" `
    -Name "Guise" -ErrorAction SilentlyContinue

# Delete the install directory (binary plus any .old/.new from a past update).
if (Test-Path $InstallDir) {
    Write-Step "Deleting $InstallDir"
    Remove-Item $InstallDir -Recurse -Force
}

$configDir = Join-Path $env:APPDATA "Guise"
Write-Host ""
Write-Host "guise uninstalled." -ForegroundColor Green
Write-Host ""
if (Test-Path $configDir) {
    Write-Host "Your rules and log were kept at:" -ForegroundColor Yellow
    Write-Host "    $configDir"
    Write-Host "  Remove them too with:  Remove-Item '$configDir' -Recurse -Force"
}
Write-Host "If guise was your default browser, Windows may still list it until you"
Write-Host "pick another browser in Settings -> Apps -> Default apps."
