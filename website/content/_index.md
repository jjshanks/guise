---
title: guise
type: docs
---

<p align="center">
  <img src="logo.png" alt="guise" width="180">
</p>

# guise

**Route every clicked link to the right Chrome profile — automatically.**

`guise` is a tiny Windows 11 app that registers itself as your default web
browser. When you click a link anywhere in Windows, it matches the URL against
an ordered list of regex rules and launches Chrome with the **profile bound to
the first matching rule**. No match → plain Chrome, exactly as before.

```text
chrome.exe --profile-directory="Profile 3" https://github.com/foo
```

One binary, three modes, a system-tray rule editor — and it writes only to
`HKEY_CURRENT_USER`, so it never needs admin rights.

## Why

If you keep separate Chrome profiles for work, personal, and a side project,
you know the dance: click a link, land in the wrong profile, copy the URL, paste
it into the right window. `guise` skips all of that.

## Highlights

- **Regex routing** — ordered rules, first match wins, full RE2 syntax.
- **Per-profile** — bind any rule to any Chrome profile you have.
- **No elevation** — HKCU only; no UAC prompt, no admin install.
- **Fails open** — bad config or a vanished profile never breaks a click; it
  just falls back to launching Chrome normally.
- **Tray editor** — add and reorder rules from a system-tray UI.
- **Self-updating** — checks GitHub Releases and updates in place.

## Get started

{{< button href="https://github.com/jjshanks/guise/releases" >}}Download the latest release{{< /button >}}

Then read the [**Specification**]({{< relref "docs/spec" >}}) for the full design,
or browse the source on [GitHub](https://github.com/jjshanks/guise).
