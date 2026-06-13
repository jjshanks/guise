# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`guise` is a Windows 11 app that registers itself as the default web browser and routes each
clicked URL to a specific Chrome profile by ordered regex rule (first match wins; no match → launch
Chrome with no `--profile-directory` flag), optionally in an incognito window. URLs can also be
rewritten by literal find/replace before launch (§15). One binary, three modes, plus a system-tray
rule editor.

`SPEC.md` is the authoritative design document — it is detailed and section-numbered (§N), and the
code comments reference those section numbers. When a design question comes up, read the relevant
SPEC section rather than guessing; when behavior and SPEC disagree, that is a bug worth surfacing.

## Build & test

```powershell
go build -ldflags "-H windowsgui" -o guise.exe .   # -H windowsgui is mandatory — without it every
                                                    # link click flashes a console window
go test ./...                                       # pure logic (config/router/chrome) runs on any OS
go vet ./...

# Opt-in: registry round-trip against real HKCU (writes + cleans up):
$env:GUISE_REGISTRY_IT=1; go test ./internal/winreg/ -run RoundTrip

# Regenerate embedded manifest + icon (committed as rsrc_windows_amd64.syso; rarely needed):
go install github.com/akavel/rsrc@latest   # rsrc must be on PATH
go generate ./...

# Canonical build: stamps the version from the nearest git tag (semver) into the binary.
./scripts/build.ps1                                 # used by CI and the release workflow
```

Go 1.26+, GOARCH amd64. Use `python.exe` (never `python3.exe`) per global instructions.

**Releases are git-tag driven.** Pushing a `v*` tag triggers `.github/workflows/release.yml`,
which builds via `scripts/build.ps1` and publishes a GitHub Release (binary + SHA256). The tag is
the single source of version truth — `internal/version` is stamped from it via `-ldflags -X`, so
`guise.exe --version` and the release always agree. Hyphenated tags (`v1.2.3-rc1`) are pre-releases.
Don't hardcode a version constant in Go; let the tag flow through `scripts/build.ps1`.

## Architecture

**One exe, three modes, dispatched by argv in `main_windows.go`.** Windows runs the *same*
registered executable for every clicked link, so each invocation must be self-contained:

| Invocation | Mode | Behavior |
|---|---|---|
| `guise.exe <url>` | ROUTE | load config, match, exec Chrome, exit immediately. The hot path. |
| `guise.exe --tray` | TRAY | long-lived: tray icon + `walk` rule editor + background GitHub-release update check (§14). The only persistent process. |
| `guise.exe --register` / `--unregister` | SETUP | write/remove HKCU registry entries, exit. |
| `guise.exe --version` (`-v`) | — | print the stamped build version (to the parent console, else a dialog) and exit. |

Two design invariants that explain the whole system — do not break them without updating SPEC:

- **ROUTE is stateless and re-reads config from disk every click.** This is why there is no
  config-reload mechanism, file watcher, or IPC: the next click picks up edits automatically. Don't
  add caching or a resident router.
- **Everything writes only to `HKEY_CURRENT_USER`.** No mode ever needs admin/elevation. Never
  introduce an HKLM write, a UAC manifest (`requireAdministrator`), or an elevated install step.
- **Routing must never fail closed.** Malformed config → route to Chrome default; a rule whose regex
  won't compile → log and skip it; a vanished profile → drop the flag. Only an unresolvable
  `chrome.exe` stops routing (with a notification). Preserve this defensiveness when editing
  `internal/router` and `internal/config`.

**Routing pipeline (`internal/router`):** ROUTE and the editor's "Test URL" preview share one
function, `Resolve` — so the preview can never drift from a real click. It runs, in order:
pre-rewrites → rule match → profile validate/fallback → delayed rewrites, then launches with the
profile flag and (if the matched rule opts in) `--incognito`.
- **Matching:** Go RE2 regex (`regexp` package — no backreferences), **unanchored** against the
  full URL, **case-sensitive** by default. `Start()` not `Run()` so ROUTE exits without waiting.
- **Rewrites (§15):** literal find/replace, all enabled ones applied in order (not first-match).
  Non-delayed rewrites run *before* matching (profile + launched URL both see the result); delayed
  ones run *after*, changing the launched URL without affecting which profile is chosen.
- **Incognito:** a per-rule flag; independent of profile fallback (a vanished profile still
  launches `--incognito`).

## Platform-split convention

The module compiles on non-Windows so the pure logic stays testable, using paired files with build
tags:

- `*_windows.go` (`//go:build windows`) — real Win32 implementation.
- `*_other.go` (`//go:build !windows`) — stub returning empty/no-op (e.g. `chrome_other.go`,
  `notify_other.go`, `main_other.go`).

When you add a function that touches Win32 (registry, shell, GUI, notifications), add **both** a
`_windows.go` implementation and an `_other.go` stub with the matching signature, or `go test ./...`
breaks on the cross-platform build. Keep pure logic (parsing, matching, profile mapping) out of the
`_windows.go` files so it remains testable everywhere.

## Package map

```
main_windows.go      mode dispatch (ROUTE / TRAY / SETUP / --version)
console_windows.go   AttachConsole helper so --version can print to the launching terminal
internal/config      config schema, load, atomic save (write temp + os.Rename)
internal/router      ordered RE2 matching + ROUTE-mode Chrome launch  ← the heart (SPEC §12)
internal/chrome      Chrome profile discovery (Local State JSON) + chrome.exe resolution (SPEC §4)
internal/winreg      HKCU registration, default-browser detection, autostart Run key (SPEC §3, §7)
internal/tray        systray menu + GUI-thread dispatch (SPEC §6.1) + background update check (SPEC §14)
internal/editor      walk rule editor + test-URL dialog (SPEC §6.2)
internal/updater     GitHub-release check, SHA256-verified download, rename-in-place self-update (SPEC §14)
internal/applog      log file + rotation; one line per click (SPEC §9)
internal/notify      Windows message-box notifications incl. Yes/No confirm (SPEC §10)
internal/winutil     shell-open helper (deep-link to ms-settings:defaultapps)
internal/version     build version stamped from git tags via -ldflags -X (pure, cross-platform)
internal/assets      go:embed tray icon
scripts/build.ps1    version-stamping build, shared by CI and the release workflow
```

Config lives at `%APPDATA%\Guise\config.json`; the log at `%APPDATA%\Guise\guise.log`.
