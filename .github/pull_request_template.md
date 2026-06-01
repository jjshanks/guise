<!-- Thanks for contributing to guise! Please fill out the sections below. -->

## Summary

<!-- What does this PR do, and why? Link any related issue: "Fixes #123". -->

## Changes

<!-- Bullet the notable changes. -->

-

## SPEC

<!-- guise is driven by SPEC.md. If behavior changed, did you update the
     relevant §N section? If this PR intentionally diverges from SPEC, explain. -->

- [ ] Behavior matches `SPEC.md` (or SPEC was updated in this PR)
- [ ] N/A — no user-visible behavior change

## Checklist

- [ ] `gofmt -l .` reports no files (code is formatted)
- [ ] `go vet ./...` passes
- [ ] `go test ./...` passes
- [ ] Win32-touching code added both a `*_windows.go` impl and an `*_other.go` stub
- [ ] No new write to `HKEY_LOCAL_MACHINE` and no UAC/elevation requirement
- [ ] `CHANGELOG.md` updated under `[Unreleased]` if user-visible

## Testing

<!-- How did you verify this? Note anything reviewers should check on a real
     Windows 11 machine that CI can't cover. -->
