# guise

A Windows 11 app that registers as the default web browser and routes each
clicked URL to a specific Chrome profile by regex rule. One binary, three
modes; a system-tray UI edits the rules. See [SPEC.md](SPEC.md) for the full
design.

## How it works

When you click a link anywhere in Windows, the shell hands the URL to
`guise.exe`. It matches the URL against an ordered list of regex rules and
launches Chrome with the profile bound to the **first matching rule**. If
nothing matches, it launches Chrome with no `--profile-directory` flag, letting
Chrome do its normal thing.

```
chrome.exe --profile-directory="Profile 3" https://github.com/foo
```

## Modes

| Invocation | Mode | Does |
|---|---|---|
| `guise.exe <url>` | ROUTE | match, launch Chrome, exit (this is what Windows runs per click) |
| `guise.exe --tray` | TRAY | tray icon + rule editor (autostart this at login) |
| `guise.exe --register` | SETUP | write HKCU registry entries so the app is an eligible browser |
| `guise.exe --unregister` | SETUP | remove those entries |

Every mode writes only to `HKEY_CURRENT_USER`, so nothing ever needs admin
rights — no UAC, no elevation.

## Build

Requires Go 1.26+ on Windows (amd64).

```powershell
go generate ./...   # regenerate rsrc_windows_amd64.syso from the manifest + icon (optional; committed)
go build -ldflags "-H windowsgui" -o guise.exe .
```

`-H windowsgui` is essential — without it every link click flashes a console
window. `go generate` needs `rsrc` on PATH: `go install github.com/akavel/rsrc@latest`.

## Install

No installer or admin rights needed:

1. Copy `guise.exe` to `%LOCALAPPDATA%\Programs\Guise\`.
2. Run `guise.exe --register` once.
3. Run `guise.exe --tray` and toggle **Start at login** in the tray menu.
4. Windows 11 forbids silent default-browser changes, so set the default
   yourself: **Settings → Apps → Default apps → Guise → Set default**
   (the tray's "Default browser: No — click to fix" item deep-links there).

## Rules

Rules live in `%APPDATA%\Guise\config.json` and are edited from the tray
("Edit rules…"). Order **is** evaluation order: first match wins.

```json
{
  "version": 1,
  "chrome_path": "",
  "rules": [
    { "id": "r1", "enabled": true, "pattern": "github\\.com/foo(/|$)", "profile_directory": "Profile 3", "comment": "GitHub foo" },
    { "id": "r2", "enabled": true, "pattern": "mail\\.google\\.com",   "profile_directory": "Profile 1", "comment": "Gmail → Work" }
  ]
}
```

- `pattern` is a **Go RE2** regex (the `regexp` package). RE2 has **no
  backreferences** — don't paste PCRE.
- Matching is **unanchored**: `github\.com/foo` also matches
  `github.com/foobar`. Anchor a boundary with `^…$` or `(/|$)`. The editor's
  "Test URL" field exists to catch over-broad patterns before they surprise you.
- Matching is **case-sensitive**; prefix `(?i)` for case-insensitive.
- `profile_directory` is the on-disk folder name (`Default`, `Profile 1`, …),
  not the friendly Chrome name. The editor's dropdown bridges the two.
- `chrome_path` empty = auto-detect.

## Diagnostics

`%APPDATA%\Guise\guise.log` records one line per click: input URL, the
rule that won (or "default"), the resolved profile, and the launch result. When
a link opens in the "wrong" profile, the log shows exactly which rule won.

## Layout

```
main_windows.go            mode dispatch (ROUTE / TRAY / SETUP)
internal/config            config schema, load, atomic save (§5)
internal/router            ordered RE2 matching + ROUTE-mode launch (§5.3, §12)
internal/chrome            profile discovery + chrome.exe resolution (§4)
internal/winreg            HKCU registration, default detection, autostart (§3, §7)
internal/tray              systray menu + GUI thread dispatch (§6.1)
internal/editor            walk rule editor + test dialog (§6.2)
internal/applog            log file + rotation (§9)
internal/notify            Windows message-box notifications (§10)
internal/winutil           shell-open helper (§3.3, §6.1)
internal/assets            embedded tray icon
```

## Tests

```powershell
go test ./...
# Registry round-trip against real HKCU (writes + cleans up; opt-in):
$env:GUISE_REGISTRY_IT=1; go test ./internal/winreg/ -run RoundTrip
```
