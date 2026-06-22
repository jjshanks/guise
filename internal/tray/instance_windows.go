//go:build windows

package tray

import "golang.org/x/sys/windows"

// instanceMutexName names the per-session mutex that marks a running tray. The
// Local\ namespace scopes it to the current login session, matching guise's
// per-user (HKCU) model: each signed-in user gets at most one tray, and
// separate sessions never collide. This is what makes --setup idempotent — a
// second tray (whether spawned by setup or launched by hand) finds the mutex
// already held and exits instead of drawing a duplicate icon (§16).
const instanceMutexName = `Local\GuiseTraySingleton`

// acquireSingleInstance creates the singleton mutex and reports whether this
// process is the first/only tray. When another instance already holds it, ok is
// false and Run must exit without drawing a second icon. The returned release
// drops the singleton (closing the handle) and is called on shutdown. Any
// unexpected error fails open (ok=true): a possible duplicate icon is a far
// smaller harm than refusing to start the only tray.
func acquireSingleInstance() (release func(), ok bool) {
	name, err := windows.UTF16PtrFromString(instanceMutexName)
	if err != nil {
		return func() {}, true
	}
	// CreateMutex returns a valid handle to the *existing* mutex alongside
	// ERROR_ALREADY_EXISTS when another instance got there first; close it so we
	// don't keep the existing one alive past our exit.
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, false
	}
	if err != nil {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return func() {}, true // Fail open: never let a guard bug suppress the tray.
	}
	return func() { windows.CloseHandle(h) }, true
}

// IsRunning reports whether a tray instance currently holds the singleton
// mutex. --setup uses it to skip spawning a redundant tray; the acquire guard in
// Run is the real backstop, so this is only an optimization (and TOCTOU here is
// harmless — a tray that starts between the check and the spawn just self-exits
// via acquireSingleInstance). It probes by opening the mutex, leaving ownership
// untouched.
func IsRunning() bool {
	name, err := windows.UTF16PtrFromString(instanceMutexName)
	if err != nil {
		return false
	}
	h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err != nil {
		return false // Absent (ERROR_FILE_NOT_FOUND) or unreadable: treat as not running.
	}
	windows.CloseHandle(h)
	return true
}
