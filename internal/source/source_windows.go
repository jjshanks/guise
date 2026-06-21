//go:build windows

package source

import (
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// snapshot reads the live process table via the Toolhelp snapshot API and
// returns a PID→{parent PID, image base name} map for walk to climb. It is
// best-effort: any failure yields nil, which walk treats as an undeterminable
// source (fail open, §5.4). No elevation is required — Toolhelp enumerates
// processes the user can see, which always includes its own ancestors.
func snapshot() map[uint32]procInfo {
	handle, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(handle)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(handle, &entry); err != nil {
		return nil
	}

	procs := make(map[uint32]procInfo)
	for {
		// ExeFile is the image base name already (e.g. "slack.exe"), but run it
		// through filepath.Base defensively in case a future API hands a path.
		name := filepath.Base(windows.UTF16ToString(entry.ExeFile[:]))
		procs[entry.ProcessID] = procInfo{ppid: entry.ParentProcessID, name: name}
		if err := windows.Process32Next(handle, &entry); err != nil {
			break // ERROR_NO_MORE_FILES at the end of the table
		}
	}
	return procs
}
