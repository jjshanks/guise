//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
)

// Apply replaces the running executable at curExe with the freshly downloaded
// newExe (both must sit in the same directory) and launches the replacement in
// TRAY mode.
//
// Windows lets you *rename* a running image but not overwrite it, so the swap
// is two renames: move the running exe aside to <exe>.old, then move the new
// file into its place. Keeping curExe's path stable matters — it is the path
// baked into the HKCU default-browser registration (SPEC §3), so a clicked link
// keeps launching the updated binary with no re-register. On failure after the
// first rename we move the old image back so we are never left with nothing at
// curExe.
//
// The caller (the tray) should quit immediately after Apply returns nil, so
// only the new process remains. The leftover <exe>.old cannot be deleted here —
// it is the image this process is still executing — so CleanupOld removes it on
// the next tray startup.
func Apply(curExe, newExe string) error {
	old := oldPath(curExe)
	_ = os.Remove(old) // Clear any leftover from a prior update.

	if err := os.Rename(curExe, old); err != nil {
		return fmt.Errorf("moving current exe aside: %w", err)
	}
	if err := os.Rename(newExe, curExe); err != nil {
		// Roll back so the registered path still resolves to a working binary.
		if rbErr := os.Rename(old, curExe); rbErr != nil {
			return fmt.Errorf("installing new exe (%w) and rollback failed: %v", err, rbErr)
		}
		return fmt.Errorf("installing new exe: %w", err)
	}

	if err := exec.Command(curExe, "--tray").Start(); err != nil {
		return fmt.Errorf("relaunching updated tray: %w", err)
	}
	return nil
}

// CleanupOld removes the <exe>.old left behind by a previous Apply. It is
// best-effort: on the very first startup after an update the old image may
// still be locked by the outgoing process, in which case the next startup
// clears it.
func CleanupOld(exe string) { _ = os.Remove(oldPath(exe)) }
