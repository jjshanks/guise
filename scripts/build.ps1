#Requires -Version 5.1
<#
.SYNOPSIS
    Build guise.exe with semantic-version metadata stamped in from git.

.DESCRIPTION
    The git tag is the single source of truth for the version (semver mechanics):

      - On an exact release tag (vX.Y.Z), `git describe --tags` returns it verbatim.
      - Past a tag, it returns e.g. v1.2.3-5-gabc1234 (5 commits after v1.2.3).
      - With no tags at all, this falls back to v0.0.0-dev+<short-sha>.

    The resulting string, the full commit SHA, and the UTC build time are stamped
    into guise/internal/version via -ldflags -X, so `guise.exe --version` and the
    tray header report exactly what was built. -H windowsgui is always included --
    without it every link click flashes a console window (SPEC §8). Both CI smoke
    builds and the release workflow call this script, so the build logic lives in
    one place.

.PARAMETER Output
    Output path for the binary. Defaults to guise.exe in the current directory.

.EXAMPLE
    ./scripts/build.ps1
.EXAMPLE
    ./scripts/build.ps1 -Output dist/guise.exe
#>
[CmdletBinding()]
param(
    [string]$Output = "guise.exe"
)

$ErrorActionPreference = "Stop"

function Get-GitVersion {
    # Prefer the most recent tag (with commits-since/dirty suffix when not exact).
    $desc = (& git describe --tags --dirty 2>$null)
    if ($LASTEXITCODE -eq 0 -and $desc) {
        return $desc.Trim()
    }
    # No tags reachable: synthesize a dev version pinned to the short SHA.
    $sha = (& git rev-parse --short HEAD 2>$null)
    if ($LASTEXITCODE -eq 0 -and $sha) {
        return "v0.0.0-dev+$($sha.Trim())"
    }
    return "v0.0.0-dev"
}

function Get-GitCommit {
    $sha = (& git rev-parse HEAD 2>$null)
    if ($LASTEXITCODE -eq 0 -and $sha) {
        return $sha.Trim()
    }
    return "none"
}

$gitVersion = Get-GitVersion
$gitCommit = Get-GitCommit
# RFC 3339 UTC, computed in a PS 5.1-compatible way (no -AsUTC).
$buildDate = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")

$pkg = "guise/internal/version"
$ldflags = @(
    "-H windowsgui"
    "-X $pkg.Version=$gitVersion"
    "-X $pkg.Commit=$gitCommit"
    "-X $pkg.Date=$buildDate"
) -join " "

$shortSha = if ($gitCommit.Length -ge 7) { $gitCommit.Substring(0, 7) } else { $gitCommit }
Write-Host "Building $Output  version=$gitVersion  commit=$shortSha  date=$buildDate"

& go build -ldflags $ldflags -o $Output .
if ($LASTEXITCODE -ne 0) {
    throw "go build failed with exit code $LASTEXITCODE"
}
Write-Host "Built $Output"
