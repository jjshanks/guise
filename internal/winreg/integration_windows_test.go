//go:build windows

package winreg

import (
	"os"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestRegisterRoundTrip exercises the full HKCU key layout against the real
// registry. It is gated behind GUISE_REGISTRY_IT=1 so a normal `go test`
// never mutates browser-registration keys; run it explicitly to verify setup.
func TestRegisterRoundTrip(t *testing.T) {
	if os.Getenv("GUISE_REGISTRY_IT") != "1" {
		t.Skip("set GUISE_REGISTRY_IT=1 to run the registry integration test")
	}
	const fakeExe = `C:\Test\guise.exe`
	t.Cleanup(func() { Unregister() })

	if err := Register(fakeExe); err != nil {
		t.Fatalf("Register: %v", err)
	}

	check := func(path, name, want string) {
		t.Helper()
		k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err != nil {
			t.Errorf("open %s: %v", path, err)
			return
		}
		defer k.Close()
		got, _, err := k.GetStringValue(name)
		if err != nil {
			t.Errorf("read %s\\%s: %v", path, name, err)
			return
		}
		if got != want {
			t.Errorf("%s\\%s = %q, want %q", path, name, got, want)
		}
	}

	check(clientKey, "", AppName)
	check(capabilitiesKey+`\URLAssociations`, "https", progID)
	check(clientKey+`\shell\open\command`, "", command(fakeExe))
	check(registeredAppsKey, regAppKey, capabilitiesKey)
	check(classesProgIDKey+`\shell\open\command`, "", command(fakeExe))

	if err := Unregister(); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	// After unregister, the client key should be gone.
	if _, err := registry.OpenKey(registry.CURRENT_USER, clientKey, registry.QUERY_VALUE); err == nil {
		t.Errorf("client key still present after Unregister")
	}
}

// TestRepairProgIDsRoundTrip verifies the stale-ProgID repair against real HKCU
// classes (#8): a ProgID whose handler exe is missing is re-pointed at the
// current exe, while a ProgID with a live handler is left untouched. It drives
// repairProgIDs directly so it never writes the real UserChoice keys. Gated
// behind GUISE_REGISTRY_IT=1 like the registration round-trip.
func TestRepairProgIDsRoundTrip(t *testing.T) {
	if os.Getenv("GUISE_REGISTRY_IT") != "1" {
		t.Skip("set GUISE_REGISTRY_IT=1 to run the registry integration test")
	}
	const (
		newExe   = `C:\Test\guise.exe`
		stalePID = "GuiseITStaleHTML"
		livePID  = "GuiseITLiveHTML"
	)
	staleCmdKey := classesKey + `\` + stalePID + `\shell\open\command`
	liveCmdKey := classesKey + `\` + livePID + `\shell\open\command`
	t.Cleanup(func() {
		for _, p := range []string{
			staleCmdKey, classesKey + `\` + stalePID + `\shell\open`, classesKey + `\` + stalePID + `\shell`, classesKey + `\` + stalePID,
			liveCmdKey, classesKey + `\` + livePID + `\shell\open`, classesKey + `\` + livePID + `\shell`, classesKey + `\` + livePID,
		} {
			registry.DeleteKey(registry.CURRENT_USER, p)
		}
	})

	// Stale: handler points at a path that does not exist. Live: handler points
	// at this test binary, which certainly exists.
	if err := setString(staleCmdKey, "", command(`C:\does\not\exist\gone.exe`)); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if err := setString(liveCmdKey, "", command(self)); err != nil {
		t.Fatalf("seed live: %v", err)
	}

	repaired := repairProgIDs(newExe, []string{stalePID, livePID, "GuiseITMissingHTML"})

	if len(repaired) != 1 || repaired[0] != stalePID {
		t.Fatalf("repaired = %v, want [%s]", repaired, stalePID)
	}
	if got, _ := readString(staleCmdKey, ""); got != command(newExe) {
		t.Errorf("stale command = %q, want %q", got, command(newExe))
	}
	if got, _ := readString(liveCmdKey, ""); got != command(self) {
		t.Errorf("live command = %q, want unchanged %q", got, command(self))
	}
}

// TestRepointProgIDsRoundTrip verifies the watchdog's recovery rewrite (§3.5,
// #14) against real HKCU classes: unlike repairProgIDs, repointProgIDs repoints a
// guise-owned ProgID whose class command points anywhere other than the current
// exe — even at a still-existing wrong binary — while leaving a class already
// launching the current exe and a system-managed ProgID (no HKCU class) untouched.
// It drives repointProgIDs directly so it never writes the real UserChoice keys.
// Gated behind GUISE_REGISTRY_IT=1 like the other round-trips.
func TestRepointProgIDsRoundTrip(t *testing.T) {
	if os.Getenv("GUISE_REGISTRY_IT") != "1" {
		t.Skip("set GUISE_REGISTRY_IT=1 to run the registry integration test")
	}
	const (
		newExe   = `C:\Test\guise.exe`
		wrongPID = "GuiseITWrongHTML" // class points at an existing-but-wrong exe
		curPID   = "GuiseITCurrentHTML"
	)
	wrongCmdKey := classesKey + `\` + wrongPID + `\shell\open\command`
	curCmdKey := classesKey + `\` + curPID + `\shell\open\command`
	t.Cleanup(func() {
		for _, p := range []string{
			wrongCmdKey, classesKey + `\` + wrongPID + `\shell\open`, classesKey + `\` + wrongPID + `\shell`, classesKey + `\` + wrongPID,
			curCmdKey, classesKey + `\` + curPID + `\shell\open`, classesKey + `\` + curPID + `\shell`, classesKey + `\` + curPID,
		} {
			registry.DeleteKey(registry.CURRENT_USER, p)
		}
	})

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	// Wrong: class points at this test binary (which exists), not newExe — so a
	// fileExists check would skip it, but repointProgIDs must still correct it.
	if err := setString(wrongCmdKey, "", command(self)); err != nil {
		t.Fatalf("seed wrong: %v", err)
	}
	// Current: class already points at newExe — must be left untouched.
	if err := setString(curCmdKey, "", command(newExe)); err != nil {
		t.Fatalf("seed current: %v", err)
	}

	repaired := repointProgIDs(newExe, []string{wrongPID, curPID, "GuiseITMissingHTML"})

	if len(repaired) != 1 || repaired[0] != wrongPID {
		t.Fatalf("repaired = %v, want [%s]", repaired, wrongPID)
	}
	if got, _ := readString(wrongCmdKey, ""); got != command(newExe) {
		t.Errorf("wrong command = %q, want %q", got, command(newExe))
	}
	if got, _ := readString(curCmdKey, ""); got != command(newExe) {
		t.Errorf("current command = %q, want unchanged %q", got, command(newExe))
	}
}

// TestReadUCProgIDRoundTrip verifies readUCProgID against both UserChoice
// layouts (#29) on a throwaway key (never the real UCPD-protected UserChoice
// keys): the legacy direct "ProgId" value, and the newer layout where the ProgID
// is nested as a "ProgId" value inside a "ProgId" subkey. The direct value wins
// when present; the nested value is the fallback. Gated behind GUISE_REGISTRY_IT=1
// like the other round-trips.
func TestReadUCProgIDRoundTrip(t *testing.T) {
	if os.Getenv("GUISE_REGISTRY_IT") != "1" {
		t.Skip("set GUISE_REGISTRY_IT=1 to run the registry integration test")
	}
	base := `SOFTWARE\GuiseSelfTest\UCProgID`
	nestedKey := base + `\` + progIDValue
	t.Cleanup(func() {
		registry.DeleteKey(registry.CURRENT_USER, nestedKey)
		registry.DeleteKey(registry.CURRENT_USER, base)
		registry.DeleteKey(registry.CURRENT_USER, `SOFTWARE\GuiseSelfTest`)
	})

	// Absent key/value reads as "" without error.
	if got, err := readUCProgID(base); got != "" || err != nil {
		t.Fatalf("readUCProgID(absent) = %q, %v; want \"\", nil", got, err)
	}

	// Nested-only layout (newer Windows 11): no direct value, ProgId subkey holds it.
	if err := setString(nestedKey, progIDValue, "GuiseHTML"); err != nil {
		t.Fatalf("seed nested: %v", err)
	}
	if got, err := readUCProgID(base); got != "GuiseHTML" || err != nil {
		t.Fatalf("readUCProgID(nested) = %q, %v; want GuiseHTML, nil", got, err)
	}

	// Direct value present (legacy layout) takes precedence over the nested subkey.
	if err := setString(base, progIDValue, "ChromeHTML"); err != nil {
		t.Fatalf("seed direct: %v", err)
	}
	if got, err := readUCProgID(base); got != "ChromeHTML" || err != nil {
		t.Fatalf("readUCProgID(direct) = %q, %v; want ChromeHTML, nil", got, err)
	}
}

// TestHandlerExeRoundTrip verifies the resolver behind IsDefault (#9) against
// real HKCU classes: a seeded ProgID's shell\open\command parses back to its
// exe, and samePath then matches the current exe against it. An absent ProgID
// resolves to "" (never the current exe). It seeds only HKCU\Classes keys — not
// the UCPD-protected UserChoice keys — so it is safe to run, and is gated behind
// GUISE_REGISTRY_IT=1 like the other round-trips.
func TestHandlerExeRoundTrip(t *testing.T) {
	if os.Getenv("GUISE_REGISTRY_IT") != "1" {
		t.Skip("set GUISE_REGISTRY_IT=1 to run the registry integration test")
	}
	const pid = "GuiseITDefaultHTML"
	cmdKey := classesKey + `\` + pid + `\shell\open\command`
	t.Cleanup(func() {
		for _, p := range []string{
			cmdKey, classesKey + `\` + pid + `\shell\open`, classesKey + `\` + pid + `\shell`, classesKey + `\` + pid,
		} {
			registry.DeleteKey(registry.CURRENT_USER, p)
		}
	})

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if err := setString(cmdKey, "", command(self)); err != nil {
		t.Fatalf("seed command: %v", err)
	}

	if got := handlerExe(pid); !samePath(got, self) {
		t.Errorf("handlerExe(%q) = %q, want samePath with %q", pid, got, self)
	}
	if got := handlerExe("GuiseITAbsentHTML"); got != "" {
		t.Errorf("handlerExe(absent) = %q, want \"\"", got)
	}

	// End-to-end: decideDefault with the real resolver treats this ProgID as
	// default in either slot.
	if !decideDefault(self, pid, pid, handlerExe) {
		t.Errorf("decideDefault with seeded ProgID in both slots = false, want true")
	}
	if !decideDefault(self, pid, "", handlerExe) {
		t.Errorf("decideDefault with seeded UserChoice only = false, want true")
	}
}
