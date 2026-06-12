# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The **git tag is the source of truth** for released versions (see the README's
"Versioning & releases" section); each release tag should have a matching entry
below.

## [Unreleased]

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

[Unreleased]: https://github.com/jjshanks/guise/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/jjshanks/guise/compare/v0.3.3...v0.4.0
[0.3.3]: https://github.com/jjshanks/guise/releases/tag/v0.3.3
