//go:build windows

package chrome

import "golang.org/x/sys/windows/registry"

// appPathsChrome reads chrome.exe's location from the App Paths registry key,
// checking HKCU first (per-user installs) then HKLM (machine-wide). Both are
// read-only lookups needing no elevation (§4.3 item 2).
func appPathsChrome() string {
	const subkey = `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, subkey, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		val, _, err := k.GetStringValue("")
		k.Close()
		if err == nil && val != "" {
			return val
		}
	}
	return ""
}
