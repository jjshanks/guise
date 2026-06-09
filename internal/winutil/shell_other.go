//go:build !windows

// Stub so the package builds off Windows, per the platform-split convention in
// CLAUDE.md. The shell-open verb is Windows-specific.
package winutil

import "errors"

// ShellOpen is a no-op stub off Windows.
func ShellOpen(target string) error {
	return errors.New("shell open is only supported on Windows")
}
