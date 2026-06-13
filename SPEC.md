# Guise — Technical Specification

A Windows 11 application, written in Go, that registers itself as the default web browser, maps incoming URLs to Chrome profiles via regex rules, and exposes a system-tray UI for editing those rules.

**Working name:** `guise` (binary: `guise.exe`)

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

The single `guise.exe` behaves differently based on how it's invoked. This is the crucial design decision — Windows invokes the *same* registered executable every time a link is clicked, so each invocation must be self-contained and self-routing.

```
guise.exe <url>          → ROUTE mode  (short-lived: match, launch Chrome, exit)
guise.exe --tray         → TRAY mode   (long-lived: tray icon + config editor)
guise.exe --register     → SETUP mode  (write HKCU registry entries, then exit)
guise.exe --unregister   → SETUP mode  (remove registry entries, then exit)
```

All registry writes target `HKEY_CURRENT_USER` (see §3), so **no mode ever needs administrator rights** — there is no elevation, no UAC prompt, no separate admin process. Setup runs as the normal user.

```
                        ┌──────────────────────────┐
   user clicks a link   │  Windows shell            │
   ───────────────────► │  default-browser dispatch │
                        └─────────────┬────────────┘
                                      │ guise.exe "https://github.com/foo"
                                      ▼
                        ┌──────────────────────────┐
                        │  ROUTE mode               │
                        │  1. load config           │
                        │  2. pre-rewrites (§15)    │
                        │  3. regex match (ordered) │
                        │  4. resolve profile        │
                        │  5. delayed rewrites (§15)│
                        │  6. exec chrome.exe        │
                        │  7. exit immediately       │
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
HKCU\SOFTWARE\Clients\StartMenuInternet\Guise
  (Default)                         = "Guise"
  \DefaultIcon
    (Default)                       = "C:\Path\guise.exe,0"
  \Capabilities
    ApplicationName                 = "Guise"
    ApplicationDescription          = "Routes URLs to Chrome profiles by regex"
    \URLAssociations
      http                          = "GuiseHTML"
      https                         = "GuiseHTML"
  \shell\open\command
    (Default)                       = "\"C:\Path\guise.exe\" \"%1\""

HKCU\SOFTWARE\RegisteredApplications
  Guise = "SOFTWARE\Clients\StartMenuInternet\Guise\Capabilities"

HKCU\SOFTWARE\Classes\GuiseHTML  (ProgID — the actual handler)
  (Default)                         = "Guise Document"
  \shell\open\command
    (Default)                       = "\"C:\Path\guise.exe\" \"%1\""
```

Note `HKCU\SOFTWARE\Classes` is the per-user equivalent of `HKEY_CLASSES_ROOT` (the OS merges them at runtime), so the ProgID registers there. No HKLM, no HKCR, no admin.

Key detail: **`%1` is the URL.** Windows substitutes the clicked URL into the command line. Quote it (`"%1"`) so URLs with `&`, spaces, or special chars survive. In Go, read it from `os.Args[1]`.

### 3.2 Default protocol handler ProgID

The `URLAssociations` map points `http`/`https` at a ProgID (`GuiseHTML`). The ProgID's `shell\open\command` is what actually runs. Both must agree on the path to the exe.

### 3.3 The Windows 11 limitation you must design around

Since Windows 10 1803 / Windows 11, apps **cannot** silently set themselves as the default handler — Microsoft moved this behind a user gesture to stop hijacking. Your `--register` step makes the app *eligible*; the user then has to:

- Open **Settings → Apps → Default apps → Guise → Set default**, or
- Click a link, and Windows shows a "How do you want to open this?" picker where Guise now appears.

**Spec requirement:** after `--register`, the tray app should detect it is not yet the default and surface a one-click **"Open Default Apps settings"** button that deep-links to `ms-settings:defaultapps`. Detect current default by reading the `ProgId` from **both** https handler records:

```
HKCU\SOFTWARE\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice\ProgId
HKCU\SOFTWARE\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoiceLatest\ProgId
```

Windows 11 24H2+ resolves clicks through `UserChoiceLatest` preferentially (see 3.4), so reading `UserChoice` alone is not enough: a healthy-looking `UserChoice = GuiseHTML` can sit beside a stale `UserChoiceLatest` ProgID that dead-ends every click, producing a false "we are default" signal (#9). Report default only when **each populated key** names a ProgID whose `HKCU\SOFTWARE\Classes\<ProgID>\shell\open\command` resolves to the **current** `guise.exe` — so a stale ProgID in either slot, or one re-pointed at a deleted binary, correctly reads as *not* default. A repaired alias (3.4) whose command now launches the current `guise.exe` counts as default, since clicks reach guise. A missing key does not constrain the verdict.

(The `UserChoice\Hash` value is intentionally tamper-protected by Windows; do **not** attempt to forge it. Read `ProgId` only to *detect* state, never to *set* it.)

### 3.4 Stale ProgIDs and `UserChoiceLatest`

Windows 11 keeps **two** handler records per scheme: the long-standing `UserChoice`, and a
newer, UCPD-protected `UserChoiceLatest` beside it:

```text
HKCU\SOFTWARE\Microsoft\Windows\Shell\Associations\UrlAssociations\<scheme>\UserChoice
HKCU\SOFTWARE\Microsoft\Windows\Shell\Associations\UrlAssociations\<scheme>\UserChoiceLatest
```

After certain updates (e.g. a Patch Tuesday reboot) Windows can begin resolving clicks via
`UserChoiceLatest` instead of `UserChoice`. If an **earlier** registration of this tool — under a
different name, e.g. `URLRouterHTML` from before the urlrouter→guise rename — is still named there,
and that ProgID's `shell\open\command` points at a binary that has since been deleted, every click
dead-ends with "Application not found" **before guise is ever invoked** (so nothing appears in
`guise.log`). Apps **cannot** write `UserChoiceLatest` (UCPD-protected, like `UserChoice\Hash`), so
the only self-service remedy is to repair the *ProgID's class*.

**Spec requirement:** `--register` and tray startup run a repair pass (`winreg.RepairStaleDefaults`)
that, for `http` and `https`, reads the ProgID named by both `UserChoice` and `UserChoiceLatest`,
and for any non-`GuiseHTML` ProgID whose `HKCU\SOFTWARE\Classes\<ProgID>\shell\open\command` points
at a missing exe, rewrites that command to launch the current `guise.exe` — turning the stale ProgID
into a working alias. It is scoped to ProgIDs that already have an HKCU class command, so
system-managed ProgIDs in HKLM (`ChromeHTML`, `MSEdgeHTM`) are never hijacked, and it fails soft
(per-ProgID errors are logged and skipped — never block registration or routing).

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
%APPDATA%\Guise\config.json
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
      "incognito": false,
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

An optional `"rewrites"` array (§15) sits alongside `"rules"`; it is omitted from the file until a rewrite is added, so existing configs are untouched:

```json
{
  "rewrites": [
    { "id": "...", "enabled": true, "find": "x.com", "replace": "xcancel.com", "delayed": false, "comment": "open X via xcancel" }
  ]
}
```

Field notes:
- `rules` order **is** evaluation order.
- `pattern` is a Go `regexp` (RE2) pattern, matched **unanchored** against the full URL string (§5.3). RE2 has no backreferences — document this so users don't paste PCRE.
- `profile_directory` stores the *directory* name (e.g. `Profile 3`); the editor shows the friendly name.
- `incognito` opens the matched URL in a private window (`--incognito`). Combined with `profile_directory` it opens an incognito window for that profile; with no profile it is just `--incognito <url>`. It is independent of the profile fallback (a vanished profile still launches incognito). Omitted from the file when false, so existing configs are untouched.
- `chrome_path` empty = auto-detect (§4.3).
- There is no default-profile field. When no rule matches, Chrome launches with no profile flag (§5.3).
- `rewrites` are literal find/replace URL transforms applied in list order; `delayed` controls whether a rewrite runs before (default) or after profile matching (§15).

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
Guise
─────────────────
✓ Default browser: Yes        (or "No — click to fix")
Edit rules…
Open config folder
─────────────────
Start at login        [toggle]
Quit
```

- **Default browser status** — live indicator from §3.3 detection. If "No", clicking opens `ms-settings:defaultapps`.
- **Edit rules…** — opens the editor window (§6.2).
- **Open config folder** — opens `%APPDATA%\Guise\` in Explorer for manual edits or log inspection.
- **Start at login** — toggles the autostart registry value (§7).

(URL testing lives in the rule editor's "Test URL" field (§6.2), not the tray menu.)

(There is deliberately no "Reload config" item: routing re-reads config on every click, and the editor re-reads on open, so there is no stale in-memory state to refresh.)

### 6.2 Rule editor window

A simple table-driven editor. Columns: `↑↓ (reorder) | Enabled | Pattern | Profile (dropdown) | Comment | Test | Delete`. Plus:
- "Add rule" button.
- Chrome path field (with auto-detect + browse).
- Live regex validation: invalid patterns flagged inline (compile with `regexp.Compile`).
- An "Open in incognito" checkbox in the selected-rule detail pane: when set, a matched URL launches with `--incognito` (combined with the profile flag when a profile is chosen).
- A "Test URL" field at the top: type a URL and the matching row highlights (or it reports "no match → Chrome default"). The preview shows `[incognito]` when the matched rule opts in, so it's visible before a real click. Because matching is unanchored, this is the primary way to catch a pattern that's broader than intended.
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
  Guise = "\"C:\Path\guise.exe\" --tray"
```

HKCU = no elevation. Only the **tray** autostarts; routing needs nothing resident.

---

## 8. Build & packaging

- **Module:** `module guise`, Go 1.22+.
- **Build:** `GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o guise.exe`
  - `-H windowsgui` suppresses the console window — essential, or every link click flashes a black box. This is the single most important build detail.
- **Manifest:** embed an application manifest requesting `asInvoker` (run as the normal user — never elevate, since all registry writes are HKCU). Mark the app per-monitor-DPI-aware here too (§10). Bundle the manifest and tray icon via a `.syso` (`github.com/akavel/rsrc`) or `go:embed`.
- **Icon:** embed a `.ico`; reference it in `DefaultIcon` and the tray.
- **Install:** because nothing needs elevation, install can be trivial — drop `guise.exe` in `%LOCALAPPDATA%\Programs\Guise\` and run `guise.exe --register`, all as the normal user. An Inno Setup script is nice-to-have for the copy + register + autostart steps, but a plain "copy the exe, run `--register` once" is sufficient. No MSI, no UAC, no admin install required.
- **winget:** guise is published to the Windows Package Manager as a **portable** package (`jjshanks.guise`), since it ships as a bare `.exe` rather than an MSI. winget drops the exe under `%LOCALAPPDATA%\Microsoft\WinGet\Packages\…` and a `guise` shim on its Links dir (PATH) — still HKCU-only, no elevation. The portable type does **not** run `--register`, so registering as the default browser stays the same manual one-time step as above. The manifests live in `packaging/winget/`; pushing a stable `v*` tag auto-opens a `microsoft/winget-pkgs` version-bump PR (`.github/workflows/winget.yml`, see `packaging/winget/README.md`). Never add an elevated/MSI install path.

---

## 9. Logging & diagnostics

- Log file: `%APPDATA%\Guise\guise.log` (rotated, small).
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

1. **ROUTE core** — hardcode one rule, prove `guise.exe <url>` launches the right Chrome profile. (Validates the whole premise fastest.)
2. **Config loader + matcher** — JSON + ordered unanchored RE2 matching + no-match-means-no-flag fallback + a "test URL" function (no GUI).
3. **Registration** — `--register`/`--unregister`, default-state detection, deep-link to settings.
4. **Tray** — icon, menu, default-browser status indicator.
5. **Editor window** — table CRUD, reorder, profile dropdown from `Local State`, atomic save, live validation.
6. **Autostart + packaging** — Run key toggle, manifest, icon embed, installer.

Each milestone is independently testable; 1–2 give you a working router from the command line before any UI exists.

---

## 12. Minimal reference: the ROUTE-mode heart

Pseudo-Go for the part everything else orbits around:

```go
func route(url string) error {
    cfg := loadConfigOrLastGood()       // never fatal

    original := url                     // the URL as clicked
    rule := "default"                   // matched rule id, or "default" on no match

    // Pre-rewrites run before matching (§15): both the match and the launched URL
    // see the rewritten string. applyRewrites is a literal find/replace over the
    // enabled rewrites whose `delayed` flag is false; an empty list is a no-op.
    url, _ = applyRewrites(cfg.Rewrites, url, false /*delayed*/)

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
            profileDir, rule = r.ProfileDirectory, r.ID
            break
        }
    }
    // A vanished or syntactically invalid profile falls back to Chrome default (§10).

    // Delayed rewrites run after matching (§15): they change the launched URL
    // without affecting which profile was chosen.
    url, rewrites := applyRewrites(cfg.Rewrites, url, true /*delayed*/)

    chrome := resolveChromePath(cfg)     // §4.3
    var args []string
    if profileDir != "" {
        args = append(args, "--profile-directory="+profileDir)
    }
    if incognito {                               // matched rule's incognito flag (§5.2)
        args = append(args, "--incognito")       // distinct argv entry; combines with profile
    }
    if url != "" {
        args = append(args, url)
    }
    err := exec.Command(chrome, args...).Start() // Start, not Run — don't wait
    // One consolidated line per click (§9): which rule won, where it routed, and
    // the final URL plus the rewrites that produced it.
    log.Printf("routed url=%q final=%q rule=%q profile=%q incognito=%v rewrites=%v chrome=%q",
        original, url, rule, profileDir, incognito, rewrites, chrome)
    return err
}
```

In the real code (`internal/router`) the pre-rewrite → match → fallback → delayed-rewrite sequence is a single pure function, `Resolve`, that both `Route` and the editor's "Test URL" preview call, so the preview can never drift from a real click. Three things this encodes: the no-match path deliberately leaves `profileDir` empty so the `--profile-directory` flag is *omitted entirely* (Chrome's default behavior); rewrites (§15) bracket the match — non-delayed before, delayed after; and `exec.Command(...).Start()` (not `Run()`) fires Chrome and lets ROUTE mode exit immediately rather than lingering as Chrome's parent.

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

---

## 14. Auto-update (checking GitHub releases)

The tray keeps itself current by checking the project's GitHub Releases for a newer build and installing it on the user's confirmation. Releases are tag-driven (see §8 / `release.yml`): pushing a `vX.Y.Z` tag publishes a `guise.exe` asset plus a `guise.exe.sha256` checksum, and `internal/version` carries the running build's tag. The updater compares the two.

### 14.1 Where it runs — TRAY only

The check lives **exclusively in TRAY mode**, never ROUTE. ROUTE is the stateless hot path (§2): it must stay fast, do no network I/O, and add nothing to a link click. The tray is the one long-lived process and the natural home for a periodic background task. This mirrors the existing default-browser status poll (§6.1).

### 14.2 The flow: check → download → verify → apply

1. **Check.** Query `GET https://api.github.com/repos/jjshanks/guise/releases/latest`. This endpoint returns the newest **stable** release — GitHub excludes pre-releases and drafts — so hyphenated tags (`v1.2.3-rc1`) never surface (decided: stable only). A `User-Agent` header is sent (GitHub requires one).
2. **Compare.** `IsNewer(current, latest)` parses both as clean release tags (`vMAJOR.MINOR.PATCH`) and compares numerically. A development build — the `dev` default, a `git describe` "ahead of tag" string like `v1.2.3-5-gabc1234`, or any build with `+metadata` — is **not** a release tag, so it never reports an available update and never even hits the API on a background tick. Developers are not nagged to "update" to an older published tag.
3. **Download + verify.** Fetch the `guise.exe` asset into the directory holding the running exe, streaming it through a SHA-256 hasher, and compare against the digest in the release's `guise.exe.sha256` asset. **A mismatch is a hard failure**: the partial file is deleted and nothing becomes installable. The verified file is named `guise.exe.new` so it can never clobber the running `guise.exe`.
4. **Apply (on the user's click).** Decided behavior: **download automatically, install on one click.** When a verified update is ready, the tray reveals an "Install update vX.Y.Z" menu item and posts a Yes/No notification: **Yes** performs the swap and relaunches immediately; **No** defers — the running version stays in use and the "Install update vX.Y.Z" menu item remains available to apply later (clicking it then asks its own Yes/No confirm). Either way the apply step is explicitly user-driven; nothing is replaced without a Yes.

### 14.3 Replacing a running, registered binary (Windows)

The exe path is baked into the HKCU default-browser registration (§3), so it must stay stable across an update — re-registering on every update would be fragile. Windows forbids overwriting a running image but **permits renaming it**, so the swap is two renames in the same directory:

1. Move the running `guise.exe` aside to `guise.exe.old`.
2. Move `guise.exe.new` into `guise.exe` (the registered path — unchanged).
3. Launch the new `guise.exe --tray`, then quit this instance so only the new tray remains.

If step 2 fails, step 1 is rolled back so the registered path always resolves to a working binary. The leftover `guise.exe.old` cannot be deleted by the outgoing process (it is the image still executing); the **next** tray startup removes it (best-effort: it may take one more start if the old process is still exiting).

### 14.4 Toggle, cadence, and failing soft

- **Toggle.** A `"auto_update"` config field (absent ⇒ enabled) backs a tray checkbox, "Check for updates automatically." A manual "Check for updates now…" item is always available regardless of the toggle and reports its outcome (up to date / downloaded / error / "development build").
- **Cadence.** One check at tray startup, then every 24 h while running. Releases are infrequent, so this keeps API traffic negligible; GitHub's unauthenticated rate limit is far above what a daily check needs.
- **Fail soft.** Like routing (§2), the update path never takes the tray down. Network errors, API failures, and checksum mismatches are logged (§9) and, for a *manual* check, shown to the user; a *background* check stays silent unless an update is actually ready. Only the explicit user-driven Apply step replaces the binary.

### 14.5 winget-installed copies defer to `winget upgrade`

When guise is installed through the Windows Package Manager (§8), the package — not guise — owns the binary's lifecycle, and a rename-in-place swap (§14.3) would desync winget's tracking. So a winget-managed copy **stands down** from self-updating: `updater.IsWingetManaged` detects the install by the `…\Microsoft\WinGet\Packages\…` segment in the running exe's path, and the tray's update check returns early before any API call or download — a *manual* check tells the user to run `winget upgrade jjshanks.guise`; a *background* check stays silent. The detection is pure path logic, so it lives in the cross-platform `internal/updater` (not a `_windows` file). A hand-installed copy is unaffected and keeps the §14.2 flow.

---

## 15. URL rewrites

A **rewrite** is a literal find-and-replace transform applied to the URL on its way to Chrome. The canonical use is host substitution — open every `https://x.com/...` link as `https://xcancel.com/...` — but Find/Replace operate on any substring, so path and query edits work too (e.g. strip a tracking parameter, or swap `/old/` for `/new/`).

Rewrites are a **separate config list from routing rules** (`"rewrites"`, alongside `"rules"`), not a variant of `Rule`. They answer a different question — *what URL should open* — than rules, which answer *which profile opens it*. Keeping them separate leaves the §5.3 matching contract untouched.

### 15.1 Semantics

1. **Literal, not regex.** Find/Replace are plain substrings; every occurrence of Find is replaced (`strings.ReplaceAll`). This is the deliberate MVP scope — regex rewrites can come later without changing the config shape (a future `"regex": true` flag).
2. **Chained, not first-match-wins.** Every enabled rewrite is applied in list order, each operating on the previous one's output. Two rewrites can therefore compose (swap the host, then strip a param). Order is editable in the tray, like rule order.
3. **Inert when blank.** A rewrite with an empty Find is skipped — an empty search string would splice Replace between every character. (The editor warns, mirroring the blank-pattern warning for rules.) A Find that simply does not occur in the URL is a harmless no-op.
4. **Disabled rewrites are skipped**, like disabled rules.

### 15.2 Timing: before vs after profile matching

Each rewrite carries a `"delayed"` flag that decides when it runs relative to profile selection:

| `delayed` | When it runs | Profile is chosen from | Chrome opens |
|---|---|---|---|
| `false` (default) | **before** matching | the **rewritten** URL | the rewritten URL |
| `true` | **after** matching | the **original** URL | the rewritten URL |

The default (before) is what you want for the `x.com → xcancel.com` case: you generally also want any `xcancel.com` routing rule to see the rewritten host. The delayed option exists for the case where the rewrite would otherwise change *which* profile a URL routes to — there you match on the original URL, then rewrite the URL that actually launches.

ROUTE order is therefore: **load config → apply non-delayed rewrites → match rules → apply delayed rewrites → launch**. The matcher and the launcher both run unchanged; rewrites only transform the string flowing between them.

### 15.3 Where it lives

The transform is a pure function in `internal/router` (`ApplyRewrites`), shared by ROUTE mode and the editor's "Test URL" preview (which now runs the full pre-rewrite → match → delayed-rewrite pipeline, so the preview matches a real click). It stays out of the hot path's failure modes: rewriting never errors, never blocks routing, and an absent/empty `"rewrites"` list routes exactly as before. The per-click log line (§9) gains `final=` (the launched URL) and `rewrites=` (the IDs that actually fired) so a rewritten URL is debuggable.

### 15.4 Editor

The rule editor (§6.2) gains a **Rewrites** tab beside **Rules**: a reorderable table (On / Find / Replace / When / Comment) with the same Add/Delete/Move controls, and a detail pane with Enabled, Find, Replace, an "Apply after profile match (delayed)" checkbox, and Comment. The shared Test URL field at the top reflects rewrites and rules together.
