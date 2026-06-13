<p align="center">
  <img src="docs/logo.png" alt="guise" width="200">
</p>

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
| `guise.exe --version` | — | print the embedded build version and exit (`-v` works too) |

Every mode writes only to `HKEY_CURRENT_USER`, so nothing ever needs admin
rights — no UAC, no elevation.

## Build

Requires Go 1.26+ on Windows (amd64).

```powershell
go generate ./...        # regenerate rsrc_windows_amd64.syso from the manifest + icon (optional; committed)
./scripts/build.ps1      # builds guise.exe with the version stamped in from git
```

`scripts/build.ps1` is the canonical build: it derives the version from the
nearest git tag (`git describe`) and stamps it, the commit, and the build date
into the binary via `-ldflags -X`, always with `-H windowsgui`. A plain
`go build -ldflags "-H windowsgui" -o guise.exe .` also works but reports its
version as `dev`.

`-H windowsgui` is essential — without it every link click flashes a console
window. `go generate` needs `rsrc` on PATH: `go install github.com/akavel/rsrc@latest`.

## Versioning & releases

Versions are [semver](https://semver.org) and the **git tag is the source of
truth**. To cut a release, push a tag:

```powershell
git tag v1.2.3
git push origin v1.2.3
```

The `Release` workflow (`.github/workflows/release.yml`) then builds `guise.exe`
with that version stamped in and publishes a GitHub Release with the binary and
its `guise.exe.sha256` checksum. Tags with a hyphen (e.g. `v1.2.3-rc1`) are
published as pre-releases. Between tags, builds report a `git describe` version
like `v1.2.3-5-gabc1234`; with no tags yet, `v0.0.0-dev+<sha>`.

Check a binary's version with `guise.exe --version`; the tray menu's header also
shows it.

## Auto-update

The tray keeps itself current (§14). At startup and once a day it checks the
[latest GitHub release](https://github.com/jjshanks/guise/releases/latest); if a
newer **stable** tag exists, it downloads `guise.exe`, verifies it against the
release's `guise.exe.sha256`, and reveals an **Install update vX.Y.Z** menu item.
Clicking it (with a confirm) swaps the binary in place — keeping the registered
path stable so the default-browser registration still points at it — and
restarts the tray.

- **Toggle:** **Check for updates automatically** (tray checkbox, on by default;
  persisted as `"auto_update"` in `config.json`). **Check for updates now…** runs
  on demand regardless of the toggle.
- Only **stable** releases trigger an update — pre-release tags (`v1.2.3-rc1`)
  are ignored. Development builds (no clean release tag) never auto-update.
- The check runs only in the tray, never on the routing hot path, and fails
  soft: a network error or checksum mismatch is logged and the tray keeps
  running. The binary is replaced only when you click Install.

## Install

No admin rights needed — everything writes to `HKEY_CURRENT_USER`. Run this in
PowerShell:

```powershell
irm https://raw.githubusercontent.com/jjshanks/guise/main/scripts/install.ps1 | iex
```

It downloads the latest `guise.exe`, **verifies it against the release's
published SHA-256**, installs it to `%LOCALAPPDATA%\Programs\Guise\`, registers
it as an eligible browser, and launches the tray. Overrides (set before the
pipe, since `iex` can't take parameters):

```powershell
# Pin a version instead of latest:
$env:GUISE_VERSION='v1.2.3'; irm https://raw.githubusercontent.com/jjshanks/guise/main/scripts/install.ps1 | iex
# Install elsewhere:
$env:GUISE_INSTALL_DIR='D:\Apps\Guise'; irm https://raw.githubusercontent.com/jjshanks/guise/main/scripts/install.ps1 | iex
```

Two manual steps remain afterward (Windows 11 forbids automating them):

1. Set the default browser: **Settings → Apps → Default apps → Guise → Set
   default** (the tray's "Default browser: No — click to fix" item deep-links
   there).
2. Toggle **Start at login** in the tray menu to autostart guise.

### Manual install

If you'd rather not pipe a script, grab `guise.exe` from the
[latest release](https://github.com/jjshanks/guise/releases/latest) and:

1. Copy `guise.exe` to `%LOCALAPPDATA%\Programs\Guise\`.
2. Run `guise.exe --register` once.
3. Run `guise.exe --tray` and toggle **Start at login** in the tray menu.
4. Set the default browser as in step 1 above.

## Uninstall

```powershell
irm https://raw.githubusercontent.com/jjshanks/guise/main/scripts/uninstall.ps1 | iex
```

This unregisters guise, removes autostart, and deletes the install directory.
Your rules and log under `%APPDATA%\Guise` are kept (the script prints how to
remove them too). Or do it by hand: `guise.exe --unregister`, untoggle **Start
at login**, then delete the install folder.

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
internal/tray              systray menu + GUI thread dispatch (§6.1) + update check (§14)
internal/editor            walk rule editor + test dialog (§6.2)
internal/updater           GitHub-release check + verified download + self-update (§14)
internal/applog            log file + rotation (§9)
internal/notify            Windows message-box notifications (§10)
internal/winutil           shell-open helper (§3.3, §6.1)
internal/version           build version stamped from git tags via -ldflags
internal/assets            embedded tray icon
scripts/build.ps1          version-stamping build (used by CI + the release workflow)
```

## Tests

```powershell
go test ./...
# Registry round-trip against real HKCU (writes + cleans up; opt-in):
$env:GUISE_REGISTRY_IT=1; go test ./internal/winreg/ -run RoundTrip
```

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the build
and test workflow, the platform-split convention, and the design invariants to
preserve. Please also read [SPEC.md](SPEC.md), the authoritative design document.

- **Code of conduct:** [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- **Reporting a vulnerability:** [SECURITY.md](SECURITY.md) (please report
  privately, not via a public issue)
- **Changelog:** [CHANGELOG.md](CHANGELOG.md)

## License

[MIT](LICENSE.md) © Joshua Shanks
