// Package version reports the build's semantic version and provenance.
//
// The three vars below are stamped at link time via -ldflags -X (see
// scripts/build.ps1 and .github/workflows/release.yml). The single source of
// truth for the version is the git tag: a release tag vX.Y.Z is what the
// release workflow embeds here, so `guise.exe --version` and the GitHub release
// always agree.
//
// This package is deliberately pure (no build tag, no Win32) so it compiles and
// is testable on every platform, per the platform-split convention in CLAUDE.md.
package version

import (
	"fmt"
	"runtime/debug"
)

// Stamped at build time. The defaults describe an un-stamped local build (a
// plain `go build` with no -ldflags), which still reports its commit via the
// runtime/debug fallback in String when built from a VCS checkout.
var (
	// Version is the semantic version, normally a git tag like "v1.2.3" or a
	// `git describe` string like "v1.2.3-5-gabc1234" for builds past a tag.
	Version = "dev"
	// Commit is the full git SHA the binary was built from.
	Commit = ""
	// Date is the build timestamp in RFC 3339 UTC.
	Date = ""
)

// Short returns the bare version string (e.g. "v1.2.3" or "dev"), suitable for
// a compact UI label.
func Short() string { return Version }

// String returns a single-line, human-readable summary, e.g.
//
//	guise v1.2.3 (abc1234, 2026-05-31T12:00:00Z)
//
// When Commit or Date were not stamped (a plain `go build`), it fills them from
// the build's embedded VCS metadata if present, so even an un-stamped build
// reports the commit it came from.
func String() string {
	commit, date := Commit, Date
	if commit == "" || date == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					if commit == "" {
						commit = s.Value
					}
				case "vcs.time":
					if date == "" {
						date = s.Value
					}
				}
			}
		}
	}

	out := "guise " + Version
	switch commit = shortSHA(commit); {
	case commit != "" && date != "":
		out += fmt.Sprintf(" (%s, %s)", commit, date)
	case commit != "":
		out += " (" + commit + ")"
	case date != "":
		out += " (" + date + ")"
	}
	return out
}

// shortSHA abbreviates a 40-char git SHA to the conventional 7 characters,
// leaving anything shorter (including the empty string) untouched.
func shortSHA(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
