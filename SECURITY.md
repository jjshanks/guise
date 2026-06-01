# Security Policy

## Reporting a vulnerability

Please report security issues **privately**. Do not open a public issue for a
suspected vulnerability.

- Preferred: open a [private security advisory](https://github.com/jjshanks/guise/security/advisories/new)
  on this repository (GitHub → **Security** → **Report a vulnerability**).
- Alternatively, email the maintainer at **jjshanks@gmail.com** with the details.

Please include enough to reproduce: the guise version (`guise.exe --version`),
your Windows version, the relevant config rule(s), and what you observed. A
proof of concept helps but is not required.

You can expect an acknowledgement within a few days. Because this is a small
volunteer-maintained project, fixes are made on a best-effort basis; we'll keep
you updated on progress and coordinate a disclosure timeline with you.

## Supported versions

This is a rolling-release project: only the **latest** GitHub release receives
security fixes. The tray's auto-update keeps installs current against the latest
stable release (see the README's "Auto-update" section).

## Security model — what to keep in mind

guise's design has a few properties that are relevant to its security posture:

- **HKCU-only, no elevation.** Every mode writes only to `HKEY_CURRENT_USER`;
  guise never requires admin rights, never writes to `HKEY_LOCAL_MACHINE`, and
  ships no UAC/elevation manifest. A report that some operation *needs* admin is
  itself a bug.
- **guise becomes your default browser.** It receives every clicked URL and
  decides which Chrome profile opens it. Bugs in URL handling, rule matching, or
  Chrome-path resolution can therefore have outsized impact — these areas get
  the most scrutiny.
- **Routing fails open by design.** Malformed config, an uncompilable regex, or
  a vanished profile must degrade gracefully (route to Chrome's default), not
  fail closed. If you find an input that crashes routing or silently drops a
  click, that's worth reporting.
- **Self-update integrity.** The tray downloads new binaries from GitHub
  releases and verifies them against the release's `guise.exe.sha256` before
  swapping in place. Weaknesses in that verification path are in scope.

## Scope

In scope: the guise binary and its three modes (ROUTE / TRAY / SETUP), the
config and registry handling, and the auto-update mechanism.

Out of scope: vulnerabilities in Google Chrome itself, in Windows, or in
third-party dependencies (report those upstream; we'll pick up fixed versions
via Dependabot).
