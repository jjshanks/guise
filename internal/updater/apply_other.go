//go:build !windows

package updater

import "errors"

// Apply is a no-op stub off Windows so the package builds and the pure logic
// stays testable everywhere. The rename-in-place swap is Windows-specific.
func Apply(curExe, newExe string) error {
	return errors.New("applying an update is only supported on Windows")
}

// CleanupOld is a no-op off Windows.
func CleanupOld(exe string) {}
