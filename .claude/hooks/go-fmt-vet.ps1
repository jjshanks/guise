# PostToolUse hook: gofmt -w the edited Go file, then `go vet` its package.
#
# Reads the Claude Code hook payload as JSON on stdin. Does nothing unless the
# edited file is a .go file that exists on disk. gofmt failures are swallowed
# (fail open); `go vet` findings are surfaced back to Claude as additionalContext
# so they can be fixed, without blocking the turn.
#
# go/gofmt are resolved from the standard Windows install first (they may not be
# on PATH) and fall back to a bare name so the hook stays portable.
#
# Note: ErrorActionPreference is left at the default (Continue). Under 'Stop',
# PowerShell 5.1 turns a native command's stderr into a terminating error, which
# would make `go vet` findings (written to stderr) vanish. We redirect vet's
# stderr to a temp file rather than merging it, for the same reason.

try {
    $raw = [Console]::In.ReadToEnd()
    if (-not $raw) { return }

    $payload = $raw | ConvertFrom-Json -ErrorAction Stop
    $filePath = $payload.tool_input.file_path
    if (-not $filePath) { return }
    if (-not $filePath.EndsWith('.go')) { return }
    if (-not (Test-Path -LiteralPath $filePath -PathType Leaf)) { return }

    # Resolve go/gofmt to ABSOLUTE paths up front (before any Push-Location).
    # cmd.exe searches the current directory first for an unqualified name, so a
    # bare 'go'/'gofmt' could be hijacked by a malicious executable dropped into
    # the edited file's package directory (e.g. an untrusted checkout). Prefer
    # the standard install; otherwise Get-Command resolves via PATH — never CWD —
    # and -CommandType Application excludes alias/function shadowing.
    $goExe = 'C:\Program Files\Go\bin\go.exe'
    if (-not (Test-Path -LiteralPath $goExe -PathType Leaf)) {
        $goExe = (Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source
    }
    $gofmtExe = 'C:\Program Files\Go\bin\gofmt.exe'
    if (-not (Test-Path -LiteralPath $gofmtExe -PathType Leaf)) {
        $gofmtExe = (Get-Command gofmt -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source
    }

    # Format in place — best effort, never let gofmt block the edit.
    if ($gofmtExe) {
        try { & $gofmtExe -w -- $filePath 2>$null | Out-Null } catch {}
    }

    # Vet the package the file lives in. `go vet` writes findings to stderr; we
    # let cmd.exe merge stderr into stdout (2>&1) so PowerShell only ever sees
    # plain text and never wraps it in NativeCommandError noise. $goExe is the
    # absolute path resolved above, so the quoted command token gives cmd.exe no
    # reason to perform a CWD-relative search.
    if (-not $goExe) { return }

    $pkgDir = Split-Path -Parent $filePath
    if (-not $pkgDir) { $pkgDir = '.' }

    $rel = $pkgDir
    try { $rel = (Resolve-Path -Relative -LiteralPath $pkgDir) -replace '\\', '/' } catch {}

    Push-Location -LiteralPath $pkgDir
    try {
        $cmdLine = '"' + $goExe + '" vet . 2>&1'
        $vetOut = (cmd /c $cmdLine | Out-String).Trim()
        $code = $LASTEXITCODE
    } finally {
        Pop-Location
    }

    if ($code -ne 0 -and $vetOut) {
        $context = "go vet reported issues in $rel after this edit:`n`n$vetOut"
        $out = @{
            hookSpecificOutput = @{
                hookEventName     = 'PostToolUse'
                additionalContext = $context
            }
        }
        $out | ConvertTo-Json -Depth 5 -Compress
    }
} catch {
    # Any unexpected failure: stay silent and fail open so routing/edits proceed.
    return
}
