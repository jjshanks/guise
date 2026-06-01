# Contributing to guise

Thanks for your interest in improving guise! This is a small, focused Windows
utility, and contributions of all sizes are welcome — bug reports, docs fixes,
and code.

By participating you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).
This project is licensed under the [MIT License](LICENSE.md); by submitting a
contribution you agree that it will be licensed under the same terms.

## Before you start

- **Read [`SPEC.md`](SPEC.md).** It is the authoritative, section-numbered
  (`§N`) design document, and the code comments reference those sections. When
  behavior and SPEC disagree, that's a bug. Changes that alter behavior should
  update the relevant SPEC section in the same PR.
- For anything beyond a trivial fix, **open an issue first** to discuss the
  approach. It saves everyone time.

## Design invariants — don't break these

guise's whole design rests on a few invariants. Don't change them without
updating `SPEC.md` and flagging it explicitly in your PR:

- **ROUTE is stateless** — it re-reads config from disk on every click. No
  caching, no file watcher, no resident router, no IPC.
- **HKCU only** — every mode writes only to `HKEY_CURRENT_USER`. Never introduce
  an `HKEY_LOCAL_MACHINE` write, a `requireAdministrator` manifest, or any step
  that needs elevation.
- **Routing fails open** — malformed config routes to Chrome's default; a rule
  whose regex won't compile is logged and skipped; a vanished profile drops the
  flag. Only an unresolvable `chrome.exe` stops routing (with a notification).

## Development setup

Requires **Go 1.26+** on **Windows (amd64)**. (The pure logic packages also
build and test on non-Windows — see the platform-split note below.)

```powershell
# Build (the -H windowsgui flag is mandatory — without it every link click
# flashes a console window):
go build -ldflags "-H windowsgui" -o guise.exe .

# Or the canonical, version-stamping build used by CI and releases:
./scripts/build.ps1
```

## Before you open a PR

Run the same checks CI runs:

```powershell
gofmt -l .       # must print nothing; run `gofmt -w .` to fix
go vet ./...
go test ./...
```

Optional registry round-trip test (writes to and cleans up real HKCU
browser-registration keys — safe, but off by default so a plain `go test`
never touches your real registration):

```powershell
$env:GUISE_REGISTRY_IT=1; go test ./internal/winreg/ -run RoundTrip
```

## The platform-split convention

The module compiles on non-Windows so the pure logic stays testable. When you
add a function that touches Win32 (registry, shell, GUI, notifications), add
**both**:

- `*_windows.go` (`//go:build windows`) — the real Win32 implementation, and
- `*_other.go` (`//go:build !windows`) — a stub with the matching signature.

Otherwise `go test ./...` breaks on the cross-platform build. Keep pure logic
(parsing, matching, profile mapping) out of the `*_windows.go` files so it stays
testable everywhere.

## Pull request guidelines

- Keep PRs focused; one logical change per PR.
- Match the style of the surrounding code; the codebase is `gofmt`-clean and
  comments reference SPEC `§N` sections.
- Update `CHANGELOG.md` under `[Unreleased]` for any user-visible change.
- Fill out the PR template checklist.

## Releases (maintainers)

Releases are **git-tag driven** and the tag is the single source of version
truth — don't hardcode a version in Go. Push a semver tag:

```powershell
git tag v1.2.3
git push origin v1.2.3
```

The release workflow builds via `scripts/build.ps1` (stamping the version from
the tag) and publishes a GitHub Release with the binary and its SHA256.
Hyphenated tags (`v1.2.3-rc1`) publish as pre-releases.
