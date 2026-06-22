package setup

import (
	"errors"
	"strings"
	"testing"
)

// fakeDeps records every side effect --setup performs so a test can assert step
// order, idempotency, and fail-soft behavior without touching HKCU, the shell,
// or spawning a tray. Each field defaults to a no-op success; a test overrides
// only what it cares about.
type fakeDeps struct {
	calls         []string // ordered record of which steps ran
	registerErr   error
	autostartErr  error
	spawnErr      error
	openErr       error
	trayRunning   bool
	isDefault     bool
	isDefaultErr  error
	spawnCount    int
	autostartOn   bool
	notifications []string
	repairedStale []string
}

func (f *fakeDeps) deps() Deps {
	return Deps{
		Register: func(exe string) error {
			f.calls = append(f.calls, "register")
			return f.registerErr
		},
		RepairStaleDefaults: func(exe string) []string {
			f.calls = append(f.calls, "repair")
			return f.repairedStale
		},
		SetAutostart: func(enabled bool, exe string) error {
			f.calls = append(f.calls, "autostart")
			f.autostartOn = enabled
			return f.autostartErr
		},
		IsDefault: func(exe string) (bool, error) {
			return f.isDefault, f.isDefaultErr
		},
		TrayRunning: func() bool {
			f.calls = append(f.calls, "trayRunning")
			return f.trayRunning
		},
		SpawnTray: func(exe string) error {
			f.calls = append(f.calls, "spawn")
			f.spawnCount++
			return f.spawnErr
		},
		OpenSettings: func(target string) error {
			f.calls = append(f.calls, "open:"+target)
			return f.openErr
		},
		Notify: func(title, message string) {
			f.notifications = append(f.notifications, message)
		},
	}
}

const testExe = `C:\Apps\Guise\guise.exe`

// TestHappyPathStepOrder verifies the steps run in the documented order (§16),
// registering before repairing, enabling autostart, spawning the tray, and
// opening the deep link.
func TestHappyPathStepOrder(t *testing.T) {
	f := &fakeDeps{repairedStale: []string{"OldHTML"}}
	if code := Run(testExe, f.deps()); code != 0 {
		t.Fatalf("Run exit code = %d, want 0", code)
	}
	want := []string{"register", "repair", "autostart", "trayRunning", "spawn", "open:" + DefaultAppsDeepLink}
	if strings.Join(f.calls, ",") != strings.Join(want, ",") {
		t.Errorf("call order = %v, want %v", f.calls, want)
	}
	if !f.autostartOn {
		t.Error("autostart was not enabled")
	}
	if f.spawnCount != 1 {
		t.Errorf("spawnCount = %d, want 1", f.spawnCount)
	}
}

// TestTrayAlreadyRunning is the idempotency case: a second --setup with a tray
// already up must not spawn another (acceptance #3, #6).
func TestTrayAlreadyRunning(t *testing.T) {
	f := &fakeDeps{trayRunning: true}
	if code := Run(testExe, f.deps()); code != 0 {
		t.Fatalf("Run exit code = %d, want 0", code)
	}
	if f.spawnCount != 0 {
		t.Errorf("spawnCount = %d, want 0 (tray already running)", f.spawnCount)
	}
	for _, c := range f.calls {
		if c == "spawn" {
			t.Fatal("spawned a second tray despite one already running")
		}
	}
}

// TestRegisterFailFailsSoft verifies a failed step 1 still runs the rest and
// returns 0 — never panics, never aborts (acceptance #9).
func TestRegisterFailFailsSoft(t *testing.T) {
	f := &fakeDeps{registerErr: errors.New("boom")}
	if code := Run(testExe, f.deps()); code != 0 {
		t.Fatalf("Run exit code = %d, want 0 even when register fails", code)
	}
	// Repair is skipped on a failed register, but autostart, tray, and deep link
	// must still run.
	for _, step := range []string{"autostart", "trayRunning", "spawn", "open:" + DefaultAppsDeepLink} {
		if !contains(f.calls, step) {
			t.Errorf("step %q did not run after register failure; calls=%v", step, f.calls)
		}
	}
	if contains(f.calls, "repair") {
		t.Error("repair ran despite register failing")
	}
	if len(f.notifications) == 0 {
		t.Error("register failure was not surfaced to the user")
	}
}

// TestEveryStepFailsStillExitsZero hammers the fail-soft contract: even with
// every boundary erroring, Run reports success and surfaces a final message.
func TestEveryStepFailsStillExitsZero(t *testing.T) {
	f := &fakeDeps{
		registerErr:  errors.New("reg"),
		autostartErr: errors.New("auto"),
		spawnErr:     errors.New("spawn"),
		openErr:      errors.New("open"),
		isDefaultErr: errors.New("isdef"),
	}
	if code := Run(testExe, f.deps()); code != 0 {
		t.Fatalf("Run exit code = %d, want 0", code)
	}
	if len(f.notifications) == 0 {
		t.Fatal("no final message surfaced")
	}
}

// TestFinalMessageAlreadyDefault: when guise is already the default there is no
// manual step to name.
func TestFinalMessageAlreadyDefault(t *testing.T) {
	f := &fakeDeps{isDefault: true}
	Run(testExe, f.deps())
	last := f.notifications[len(f.notifications)-1]
	if !strings.Contains(last, "already your default") {
		t.Errorf("final message %q should note guise is already default", last)
	}
	if strings.Contains(last, "Set default") {
		t.Errorf("final message %q should not nag about the manual step when already default", last)
	}
}

// TestFinalMessageDeepLinkFallback: when the deep link fails, the final message
// recites the full manual click-path so the user isn't stranded.
func TestFinalMessageDeepLinkFallback(t *testing.T) {
	f := &fakeDeps{openErr: errors.New("no shell")}
	Run(testExe, f.deps())
	last := f.notifications[len(f.notifications)-1]
	if !strings.Contains(last, manualPath) {
		t.Errorf("final message %q should recite the manual path when the deep link fails", last)
	}
}

// TestFinalMessagePointsAtSettings: the normal not-yet-default path points at
// the just-opened Settings window and names only the manual step (acceptance #5).
func TestFinalMessagePointsAtSettings(t *testing.T) {
	f := &fakeDeps{}
	Run(testExe, f.deps())
	last := f.notifications[len(f.notifications)-1]
	if !strings.Contains(last, "Set default") {
		t.Errorf("final message %q should name the Set default step", last)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
