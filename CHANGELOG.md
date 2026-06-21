# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The **git tag is the source of truth** for released versions (see the README's
"Versioning & releases" section); each release tag should have a matching entry
below.

## [Unreleased]

## [0.7.0] - 2026-06-21

### Added
- Source-app matching: a rule can now match on the **application that produced
  the click** via an optional `source` field — a case-insensitive substring of
  the originating process's image name (e.g. `slack` matches `Slack.exe`). A rule
  with both `pattern` and `source` requires both to match; a `source`-only rule
  matches any URL from that app. The source is resolved per click by walking the
  process tree and skipping OS brokers (`explorer.exe`, `RuntimeBroker.exe`, …);
  it is best-effort and fails open, so a click is never blocked when the source
  can't be determined. Exposed in the rule editor as a "Source app" field, with a
  "from app" simulator next to Test URL. Documented as SPEC §5.4 (#16).

## [0.6.1] - 2026-06-21

### Fixed
- Default-browser detection now reads the authoritative `UserChoiceLatest`
  ProgID, fixing false "not default" reports when Windows records the choice in a
  nested ProgID (#29).

## [0.6.0] - 2026-06-21

### Added
- TRAY default-browser watchdog: the tray polls default-browser status and
  raises an actionable notification on a healthy→broken transition, repointing a
  stale guise-owned ProgID when it can (SPEC §3.5, #14).

### Changed
- Bump `golang.org/x/sys` from 0.45.0 to 0.46.0.

## [0.5.0] - 2026-06-12

### Added
- Per-rule "Open in incognito" option: a matched URL launches with `--incognito`
  (combined with the profile flag when one is chosen), independent of the profile
  fallback (#19).
- winget distribution: install and upgrade guise via `winget` (#18); a
  winget-installed copy defers self-update to `winget upgrade` (SPEC §14.5).
- Internet install/uninstall scripts for a one-line setup.

## [0.4.0] - 2026-06-12

### Added
- URL rewrite rules: literal find/replace transforms applied to the clicked URL
  before it is launched — e.g. `x.com` → `xcancel.com`, plus arbitrary path and
  query edits. Rewrites chain in list order, and each carries a `delayed` flag
  that controls timing relative to profile selection: by default a rewrite runs
  *before* matching (so both the chosen profile and the launched URL see the new
  string); when delayed, it runs *after* matching (the profile is chosen from the
  original URL, while Chrome opens the rewritten one). Managed from a new
  "Rewrites" tab in the rule editor, and the Test URL field previews the full
  pre-rewrite → match → delayed-rewrite pipeline. Documented as SPEC §15.

## [0.3.3] - 2026-05-31

### Added
- Project governance and community docs: code of conduct, contributing guide,
  security policy, issue/PR templates.

[Unreleased]: https://github.com/jjshanks/guise/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/jjshanks/guise/compare/v0.6.1...v0.7.0
[0.6.1]: https://github.com/jjshanks/guise/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/jjshanks/guise/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/jjshanks/guise/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/jjshanks/guise/compare/v0.3.3...v0.4.0
[0.3.3]: https://github.com/jjshanks/guise/releases/tag/v0.3.3
