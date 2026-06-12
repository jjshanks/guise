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
