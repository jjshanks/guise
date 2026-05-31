# URL Router — Technical Specification

A Windows 11 application, written in Go, that registers itself as the default web browser, maps incoming URLs to Chrome profiles via regex rules, and exposes a system-tray UI for editing those rules.

**Working name:** `urlrouter` (binary: `urlrouter.exe`)

---

## 1. Problem & behavior

When the OS hands a URL to the default browser, this app intercepts it, matches the URL against an ordered list of regex rules, and launches Chrome with the profile bound to the first matching rule. **If nothing matches, it launches Chrome with no `--profile-directory` flag**, letting Chrome fall back to its own normal behavior (the last-focused profile window).

**Canonical example**

| URL | Matched rule | Chrome launch |
|---|---|---|
| `https://github.com/foo` | `github\.com/foo` → `Profile 3` | `chrome.exe --profile-directory="Profile 3" <url>` |
| `https://github.com/bar` | (no rule matches) | `chrome.exe <url>` — Chrome's default behavior |
| `https://mail.google.com/...` | `mail\.google\.com` → `Profile 1` | `chrome.exe --profile-directory="Profile 1" <url>` |

Rules are evaluated **top to bottom, first match wins**. This ordering is the entire mental model the user needs, so the UI must make order obvious and editable.

Patterns are **unanchored** (see §5.3) — `github\.com/foo` matches anywhere in the URL string, so it would also catch `github.com/foobar`. Anchor with `^…$` when you need an exact boundary.

---

## 2. Architecture: one binary, three modes

The single `urlrouter.exe` behaves differently based on how it's invoked. This is the crucial design decision — Windows invokes the *same* registered executable every time a link is clicked, so each invocation must be self-contained and self-routing.

```
urlrouter.exe <url>          → ROUTE mode  (short-lived: match, launch Chrome, exit)
urlrouter.exe --tray         → TRAY mode   (long-lived: tray icon + config editor)
urlrouter.exe --register     → SETUP mode  (write HKCU registry entries, then exit)
urlrouter.exe --unregister   → SETUP mode  (remove registry entries, then exit)
```

All registry writes target `HKEY_CURRENT_USER` (see §3), so **no mode ever needs administrator rights** — there is no elevation, no UAC prompt, no separate admin process. Setup runs as the normal user.

```
                        ┌──────────────────────────┐
   user clicks a link   │  Windows shell            │
   ───────────────────► │  default-browser dispatch │
                        └─────────────┬────────────┘
                                      │ urlrouter.exe "https://github.com/foo"
                                      ▼
                        ┌──────────────────────────┐
                        │  ROUTE mode               │
                        │  1. load config           │
                        │  2. regex match (ordered) │
                        │  3. resolve profile        │
                        │  4. exec chrome.exe        │
                        │  5. exit immediately       │
                        └─────────────┬────────────┘
                                      │ chrome.exe --profile-directory="Profile 3" <url>
                                      ▼
                                   Chrome

   separately, at login:
                        ┌──────────────────────────┐
                        │  TRAY mode (autostart)    │
                        │  - tray icon + menu       │
                        │  - opens config editor    │
                        └──────────────────────────┘
```

Why split this way:
- **ROUTE mode is stateless.** Every link click spawns a fresh process that reads config from disk, matches, execs Chrome, and exits. Statelessness is the point: no shared mutable state, no "is the tray running?" dependency, no IPC between the router and anything else. (Performance is a non-issue — a Go binary reading a small JSON file and running a handful of RE2 regexes adds single-digit milliseconds; the only perceptible cost is Chrome's own launch, which you don't control. The one real foot-gun is forgetting `-H windowsgui` at build time, which would flash a console window on every click — see §8.)
- **TRAY mode is the only persistent process.** It owns the icon and the editor window. It does *not* need to be running for routing to work — routing reads config from disk on each invocation.
- **Because ROUTE re-reads config on every click, there is no config-reload problem to solve.** Edits made in the tray editor are picked up by the very next click automatically. No file watcher, no reload command, no in-memory cache to invalidate.

---

## 3. Becoming the default browser (Windows 11 specifics)

This is the part with the most platform-specific friction, so it gets its own section.

### 3.1 Registry registration (HKCU only)

Windows 11 discovers browsers through **registered application capabilities**. You write a set of registry keys, then ask the user to confirm the default via Settings (Win 11 does **not** allow programmatic silent default-browser changes — see 3.3).

Because this is a single-user personal tool, **every key lives under `HKEY_CURRENT_USER`** — Windows fully supports per-user browser registration there, and writing to HKCU needs no elevation. The mirror of the system-wide locations under HKCU is:

```
HKCU\SOFTWARE\Clients\StartMenuInternet\URLRouter
  (Default)                         = "URL Router"
  \DefaultIcon
    (Default)                       = "C:\Path\urlrouter.exe,0"
  \Capabilities
    ApplicationName                 = "URL Router"
    ApplicationDescription          = "Routes URLs to Chrome profiles by regex"
    \URLAssociations
      http                          = "URLRouterHTML"
      https                         = "URLRouterHTML"
  \shell\open\command
    (Default)                       = "\"C:\Path\urlrouter.exe\" \"%1\""

HKCU\SOFTWARE\RegisteredApplications
  URLRouter = "SOFTWARE\Clients\StartMenuInternet\URLRouter\Capabilities"

HKCU\SOFTWARE\Classes\URLRouterHTML  (ProgID — the actual handler)
  (Default)                         = "URL Router Document"
  \shell\open\command
    (Default)                       = "\"C:\Path\urlrouter.exe\" \"%1\""
```

Note `HKCU\SOFTWARE\Classes` is the per-user equivalent of `HKEY_CLASSES_ROOT` (the OS merges them at runtime), so the ProgID registers there. No HKLM, no HKCR, no admin.

Key detail: **`%1` is the URL.** Windows substitutes the clicked URL into the command line. Quote it (`"%1"`) so URLs with `&`, spaces, or special chars survive. In Go, read it from `os.Args[1]`.

### 3.2 Default protocol handler ProgID

The `URLAssociations` map points `http`/`https` at a ProgID (`URLRouterHTML`). The ProgID's `shell\open\command` is what actually runs. Both must agree on the path to the exe.

### 3.3 The Windows 11 limitation you must design around

Since Windows 10 1803 / Windows 11, apps **cannot** silently set themselves as the default handler — Microsoft moved this behind a user gesture to stop hijacking. Your `--register` step makes the app *eligible*; the user then has to:

- Open **Settings → Apps → Default apps → URL Router → Set default**, or
- Click a link, and Windows shows a "How do you want to open this?" picker where URL Router now appears.

**Spec requirement:** after `--register`, the tray app should detect it is not yet the default and surface a one-click **"Open Default Apps settings"** button that deep-links to `ms-settings:defaultapps`. Detect current default by reading:

```
HKCU\SOFTWARE\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice
  ProgId = <should be "URLRouterHTML" when we are default>
```

(The `UserChoice\Hash` value is intentionally tamper-protected by Windows; do **not** attempt to forge it. Read `ProgId` only to *detect* state, never to *set* it.)

---

## 4. Chrome profile resolution

### 4.1 How Chrome profiles map

Chrome launches a specific profile via:

```
chrome.exe --profile-directory="Profile 3" "https://github.com/foo"
```

The `--profile-directory` value is the **on-disk folder name** (`Default`, `Profile 1`, `Profile 2`, …) — *not* the friendly display name the user sees in Chrome. The app must bridge friendly-name → directory-name.

### 4.2 Discovering available profiles

Read Chrome's `Local State` file (JSON) at:

```
%LOCALAPPDATA%\Google\Chrome\User Data\Local State
```

The relevant slice:

```json
{
  "profile": {
    "info_cache": {
      "Default":   { "name": "Personal", "gaia_name": "..." },
      "Profile 1": { "name": "Work",     "gaia_name": "..." },
      "Profile 3": { "name": "foo",      "gaia_name": "..." }
    }
  }
}
```

So `info_cache` maps **directory name → { friendly name }**. The tray editor uses this to present a dropdown of friendly names while storing the directory name in config. Re-read on editor open so newly-created profiles appear.

### 4.3 Locating chrome.exe

Resolution order (first found wins):
1. Explicit `chrome_path` in config, if set.
2. `App Paths\chrome.exe` registry key → `(Default)`. Check `HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe` first (per-user Chrome installs), then `HKLM\...` (machine-wide installs) — both are read-only lookups, no elevation needed.
3. Common fallbacks: `%ProgramFiles%\Google\Chrome\Application\chrome.exe`, `%ProgramFiles(x86)%\...`, `%LOCALAPPDATA%\Google\Chrome\Application\chrome.exe`.

---

## 5. Configuration

### 5.1 Location

```
%APPDATA%\URLRouter\config.json
```

`%APPDATA%` (roaming) so it follows the user; no elevation needed to write it.

### 5.2 Schema

```json
{
  "version": 1,
  "chrome_path": "",
  "rules": [
    {
      "id": "uuid-or-stable-key",
      "enabled": true,
      "pattern": "github\\.com/foo(/|$)",
      "profile_directory": "Profile 3",
      "comment": "GitHub foo org → foo profile"
    },
    {
      "id": "...",
      "enabled": true,
      "pattern": "mail\\.google\\.com",
      "profile_directory": "Profile 1",
      "comment": "Gmail → Work"
    }
  ]
}
```

Field notes:
- `rules` order **is** evaluation order.
- `pattern` is a Go `regexp` (RE2) pattern, matched **unanchored** against the full URL string (§5.3). RE2 has no backreferences — document this so users don't paste PCRE.
- `profile_directory` stores the *directory* name (e.g. `Profile 3`); the editor shows the friendly name.
- `chrome_path` empty = auto-detect (§4.3).
- There is no default-profile field. When no rule matches, Chrome launches with no profile flag (§5.3).

### 5.3 Matching semantics (the behavioral contract)

1. Iterate `rules` in order.
2. Skip if `enabled == false`.
3. Compile `pattern`. (In ROUTE mode each pattern is compiled at most once per run and matching stops at first hit, so there's nothing to cache — compile lazily as you iterate.)
4. First rule whose pattern matches the URL → launch its `profile_directory`, stop.
5. **No match → launch `chrome.exe <url>` with no `--profile-directory` flag**, deferring to Chrome's own default behavior.
6. If a pattern fails to compile → log a warning, skip that rule, continue. A broken rule must never break routing.

Locked-in decisions:
- **Matching is unanchored** (`regexp.MatchString` semantics): the pattern matches if it occurs *anywhere* in the URL. `github\.com/foo` therefore also matches `github.com/foobar`, `github.com/foo-archive`, etc. To pin a boundary, the user anchors explicitly — e.g. `github\.com/foo(/|$)` or `^https://github\.com/foo$`. This is the documented foot-gun; the editor's test panel (§6) exists to make it visible before it surprises you.
- Matching is **case-sensitive** by default; users prefix `(?i)` for case-insensitive patterns.

---

## 6. System tray UI

### 6.1 Tray menu (right-click)

```
URL Router
─────────────────
✓ Default browser: Yes        (or "No — click to fix")
Edit rules…
Open config folder
─────────────────
Test a URL…
─────────────────
Start at login        [toggle]
Quit
```

- **Default browser status** — live indicator from §3.3 detection. If "No", clicking opens `ms-settings:defaultapps`.
- **Edit rules…** — opens the editor window (§6.2).
- **Open config folder** — opens `%APPDATA%\URLRouter\` in Explorer for manual edits or log inspection.
- **Test a URL…** — input box; shows which rule would match (or "no match → Chrome default") and which profile would launch, *without* launching. Critical for debugging unanchored patterns without clicking real links.
- **Start at login** — toggles the autostart registry value (§7).

(There is deliberately no "Reload config" item: routing re-reads config on every click, and the editor re-reads on open, so there is no stale in-memory state to refresh.)

### 6.2 Rule editor window

A simple table-driven editor. Columns: `↑↓ (reorder) | Enabled | Pattern | Profile (dropdown) | Comment | Test | Delete`. Plus:
- "Add rule" button.
- Chrome path field (with auto-detect + browse).
- Live regex validation: invalid patterns flagged inline (compile with `regexp.Compile`).
- A "Test URL" field at the top: type a URL and the matching row highlights (or it reports "no match → Chrome default"). Because matching is unanchored, this is the primary way to catch a pattern that's broader than intended.
- Save writes config.json atomically (write temp file in same dir, `os.Rename`).

No default-profile control — the no-match case is fixed behavior (launch Chrome with no profile flag) and needs no configuration.

### 6.3 GUI libraries (decided)

| Need | Library | Notes |
|---|---|---|
| Tray icon + menu | `github.com/getlantern/systray` | Mature, minimal, Windows-friendly. |
| Editor window | `github.com/lxn/walk` | Win32-native: small binary, native Windows look, good table/grid support. |

Decided on **`systray` + `walk`**. Both are Win32-native, keeping the binary small and the UI looking like a real Windows app. Cross-platform is explicitly out of scope, so the heavier alternatives (e.g. Fyne) were rejected — see §13.

---

## 7. Autostart (tray at login)

Toggle a value under:

```
HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Run
  URLRouter = "\"C:\Path\urlrouter.exe\" --tray"
```

HKCU = no elevation. Only the **tray** autostarts; routing needs nothing resident.

---

## 8. Build & packaging

- **Module:** `module urlrouter`, Go 1.22+.
- **Build:** `GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o urlrouter.exe`
  - `-H windowsgui` suppresses the console window — essential, or every link click flashes a black box. This is the single most important build detail.
- **Manifest:** embed an application manifest requesting `asInvoker` (run as the normal user — never elevate, since all registry writes are HKCU). Mark the app per-monitor-DPI-aware here too (§10). Bundle the manifest and tray icon via a `.syso` (`github.com/akavel/rsrc`) or `go:embed`.
- **Icon:** embed a `.ico`; reference it in `DefaultIcon` and the tray.
- **Install:** because nothing needs elevation, install can be trivial — drop `urlrouter.exe` in `%LOCALAPPDATA%\Programs\URLRouter\` and run `urlrouter.exe --register`, all as the normal user. An Inno Setup script is nice-to-have for the copy + register + autostart steps, but a plain "copy the exe, run `--register` once" is sufficient. No MSI, no UAC, no admin install required.

---

## 9. Logging & diagnostics

- Log file: `%APPDATA%\URLRouter\urlrouter.log` (rotated, small).
- ROUTE mode logs: timestamp, input URL, matched rule id (or "default"), resolved profile, chrome path, launch result. One line per click.
- This log is the primary debugging surface — when a link opens in the "wrong" profile, the log shows exactly which rule won.

---

## 10. Edge cases to handle

| Case | Required behavior |
|---|---|
| No argument in ROUTE mode | Launch Chrome with no URL and no profile flag (just opens Chrome normally). |
| Chrome not installed / path unresolved | Show a Windows notification; fall back to nothing (don't crash silently). |
| `profile_directory` no longer exists | Launch with no profile flag (Chrome default) and warn in log + next tray open. |
| Malformed config.json | Load last-good in-memory copy if available; otherwise route everything with no profile flag (Chrome default) and surface an error in the tray. Never block routing on bad config. |
| Two Chrome windows race | Fine — Chrome dedupes by profile; passing a URL to an already-running profile opens a tab. |
| Non-http(s) scheme handed to us | Pass through to Chrome unchanged. |
| Very long / weird URLs | Always pass via argv (`"%1"`), never via a shell string, to avoid quoting injection. |
| Multiple monitors / DPI | Editor window must be per-monitor-DPI-aware (manifest setting, §8). |

---

## 11. Milestones (suggested build order)

1. **ROUTE core** — hardcode one rule, prove `urlrouter.exe <url>` launches the right Chrome profile. (Validates the whole premise fastest.)
2. **Config loader + matcher** — JSON + ordered unanchored RE2 matching + no-match-means-no-flag fallback + a "test URL" function (no GUI).
3. **Registration** — `--register`/`--unregister`, default-state detection, deep-link to settings.
4. **Tray** — icon, menu, default-browser status indicator, test-a-URL dialog.
5. **Editor window** — table CRUD, reorder, profile dropdown from `Local State`, atomic save, live validation.
6. **Autostart + packaging** — Run key toggle, manifest, icon embed, installer.

Each milestone is independently testable; 1–2 give you a working router from the command line before any UI exists.

---

## 12. Minimal reference: the ROUTE-mode heart

Pseudo-Go for the part everything else orbits around:

```go
func route(url string) error {
    cfg := loadConfigOrLastGood()       // never fatal

    profileDir := ""                    // "" means: no --profile-directory flag
    for _, r := range cfg.Rules {
        if !r.Enabled {
            continue
        }
        re, err := regexp.Compile(r.Pattern) // unanchored; skip-on-error
        if err != nil {
            log.Printf("skipping rule %s: bad pattern: %v", r.ID, err)
            continue
        }
        if re.MatchString(url) {
            profileDir = r.ProfileDirectory
            log.Printf("matched rule %s → profile %q", r.ID, profileDir)
            break
        }
    }
    if profileDir == "" {
        log.Printf("no rule matched %q → Chrome default", url)
    }

    chrome := resolveChromePath(cfg)     // §4.3
    var args []string
    if profileDir != "" {
        args = append(args, "--profile-directory="+profileDir)
    }
    if url != "" {
        args = append(args, url)
    }
    return exec.Command(chrome, args...).Start() // Start, not Run — don't wait
}
```

Two things this encodes: the no-match path deliberately leaves `profileDir` empty so the `--profile-directory` flag is *omitted entirely* (Chrome's default behavior), and `exec.Command(...).Start()` (not `Run()`) fires Chrome and lets ROUTE mode exit immediately rather than lingering as Chrome's parent.

---

## 13. Rejected alternatives (so we don't relitigate)

**A Chrome extension instead of an external binary.** Considered and rejected — it cannot do the job at any of the three requirements:
- *Be the default browser:* an extension is code running inside an already-launched Chrome. It has no way to register an executable as a Windows URL handler, so it can never receive links clicked in other apps (Slack, email, etc.). The default-browser requirement alone disqualifies it.
- *Route between profiles:* this is the fatal one. Extensions are sandboxed **per profile** and have no API to see, message, or launch another profile. There is no `tabs.moveToProfile()` and there won't be — cross-profile isolation is the whole point of profiles. The core feature is structurally outside what an extension can do.
- *Tray + config:* extensions get a toolbar popup, not a Windows tray icon.
- Even the "native messaging" hybrid (extension → external native host) was rejected: it still requires the external binary to do all the real work, still can't catch out-of-Chrome links (so you'd need to be the default browser *anyway*), and only handles the subset of links originating inside Chrome. It adds a moving part and buys nothing. The clean insight: routing must happen **upstream of Chrome**, at the OS handoff — which only an external default-browser binary sits at.

**HKLM (machine-wide) registration.** Rejected in favor of HKCU-only. This is a single-user personal tool; per-user registration under `HKCU` is fully supported by Windows for browser defaults and needs no elevation, which removes the entire admin-mode / UAC / elevated-installer apparatus.

**A configurable default profile for the no-match case.** Rejected. When no rule matches, the app launches Chrome with no `--profile-directory` flag and lets Chrome do its normal thing (last-focused profile window). This matches the existing mental model and removes a config field and an editor control.

**Fyne (or other cross-platform GUI).** Rejected. Cross-platform is out of scope; `walk` gives a smaller, more native-feeling Windows binary.

**Config hot-reload / file watcher / "Reload config" menu item.** Rejected as solving a non-problem. ROUTE mode re-reads config from disk on every single click (it's a fresh process each time), and the tray editor re-reads on open. There is never stale in-memory config to refresh.

**Anchored-by-default matching.** Rejected in favor of unanchored. Unanchored is more convenient for the common case of host/path fragments; the documented trade-off (a pattern matching more broadly than intended) is surfaced by the editor's test panel rather than prevented by forcing anchors.
