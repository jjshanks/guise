// Package setup implements --setup (§16): the one-command onboarding that
// collapses the manual register → autostart → launch-tray → set-default dance
// into a single invocation. It orchestrates the existing winreg/winutil/tray
// boundaries — it never reimplements registry writes — and hands the user to
// the one step Windows 11 forbids automating: choosing the default browser.
//
// The orchestration is pure and platform-agnostic: every side effect is an
// injected function (Deps), so the step order, idempotency, and fail-soft
// behavior are unit-testable with fakes and the package compiles everywhere.
// main_windows.go wires the real Win32 implementations; non-Windows builds
// never reach here (the whole binary is a stub, like the other SETUP modes).
package setup

import "log"

// DefaultAppsDeepLink is the Settings URI --setup opens so the user can pick
// Guise as their default browser — the same deep link the tray's "Default
// browser: No — click to fix" item uses (§3.3). Reusing it keeps the two paths
// in sync.
const DefaultAppsDeepLink = "ms-settings:defaultapps"

// manualPath is the click-path to recite when the deep link can't be opened, so
// a failed step 4 still tells the user exactly where to go (the fallback tail of
// §16's deep-link chain).
const manualPath = "Settings → Apps → Default apps → Guise → Set default"

// Deps are the side-effecting boundaries of --setup, injected so the
// orchestration stays testable with fakes (§16). Each mirrors an existing
// package function; main_windows.go supplies the real ones.
type Deps struct {
	// Register writes the HKCU default-browser entries — the same routine as
	// --register (winreg.Register). Must not be reimplemented here.
	Register func(exe string) error
	// RepairStaleDefaults re-points stale ProgIDs at exe, mirroring what
	// --register does after registering (winreg.RepairStaleDefaults).
	RepairStaleDefaults func(exe string) []string
	// SetAutostart enables (or disables) the login Run entry (winreg.SetAutostart, §7).
	SetAutostart func(enabled bool, exe string) error
	// IsDefault reports whether guise is already the https handler, so the final
	// message can skip the manual step when it's unnecessary (winreg.IsDefault).
	IsDefault func(exe string) (bool, error)
	// TrayRunning reports whether a tray already holds the single-instance guard
	// (tray.IsRunning), so setup doesn't spawn a redundant one.
	TrayRunning func() bool
	// SpawnTray launches "<exe> --tray" detached (the tray's single-instance
	// guard makes a redundant spawn self-exit, so this is always safe to call).
	SpawnTray func(exe string) error
	// OpenSettings opens a shell target — here the default-apps deep link
	// (winutil.ShellOpen).
	OpenSettings func(target string) error
	// Notify surfaces a message box to the user (notify.Info).
	Notify func(title, message string)
}

// Run performs the --setup steps in order, each idempotent and fail-soft, and
// returns the process exit code. It always returns 0: --setup is best-effort
// onboarding, and a single failed step is logged and surfaced but never aborts
// the rest (a failed register still lets autostart + tray + deep-link run, so
// the user is left as close to done as possible). Nothing here panics, and
// every step's outcome lands in guise.log so a failed setup is reconstructable.
func Run(exe string, d Deps) int {
	log.Printf("setup: starting for exe=%q", exe)

	// Step 1: register the HKCU default-browser entries (idempotent — re-running
	// overwrites the same values). On failure, keep going: the remaining steps
	// are still worth doing.
	if err := d.Register(exe); err != nil {
		log.Printf("setup: register failed: %v", err)
		d.Notify(appName, "Setup could not register Guise as a browser:\n\n"+err.Error()+"\n\nThe remaining setup steps will still run.")
	} else {
		log.Printf("setup: registered exe=%q", exe)
		// Heal any stale ProgIDs from earlier installs, exactly as --register does.
		if repaired := d.RepairStaleDefaults(exe); len(repaired) > 0 {
			log.Printf("setup: repaired stale ProgIDs: %v", repaired)
		}
	}

	// Step 2: enable start-at-login (idempotent — writes the same Run value).
	if err := d.SetAutostart(true, exe); err != nil {
		log.Printf("setup: enable start-at-login failed: %v", err)
	} else {
		log.Printf("setup: enabled start-at-login")
	}

	// Step 3: spawn the tray detached, unless one is already running. The tray's
	// own single-instance guard is the real backstop, so even if TrayRunning
	// races, a second tray self-exits — exactly one survives either way.
	switch {
	case d.TrayRunning():
		log.Printf("setup: tray already running; not spawning a second instance")
	default:
		if err := d.SpawnTray(exe); err != nil {
			log.Printf("setup: spawn tray failed: %v", err)
			d.Notify(appName, "Setup could not start the Guise tray:\n\n"+err.Error()+"\n\nYou can start it later by running: guise --tray")
		} else {
			log.Printf("setup: spawned tray")
		}
	}

	// Step 4: open the default-apps Settings page so the user can finish the one
	// step Windows 11 forbids automating. A failure isn't fatal — step 5 recites
	// the manual path instead.
	deepLinkOK := true
	if err := d.OpenSettings(DefaultAppsDeepLink); err != nil {
		deepLinkOK = false
		log.Printf("setup: open default-apps settings failed: %v", err)
	} else {
		log.Printf("setup: opened %s", DefaultAppsDeepLink)
	}

	// Step 5: tell the user the one manual step that remains (if any).
	d.Notify(appName, finalMessage(exe, deepLinkOK, d.IsDefault))
	log.Printf("setup: complete")
	return 0
}

const appName = "Guise"

// finalMessage names only the manual step the user must still do. If guise is
// already the default browser there is nothing left to do; otherwise it points
// at the just-opened Settings window, or — if the deep link failed — recites the
// full click-path so the user is never stranded.
func finalMessage(exe string, deepLinkOK bool, isDefault func(exe string) (bool, error)) string {
	if isDef, err := isDefault(exe); err == nil && isDef {
		return "Guise is registered and running, and it's already your default browser.\n\nSetup is complete."
	}
	if deepLinkOK {
		return "Guise is registered and running.\n\nLast step: in the Windows Settings window that just opened, pick Guise and choose “Set default.”"
	}
	return "Guise is registered and running.\n\nLast step: open " + manualPath + ", pick Guise and choose “Set default.”"
}
