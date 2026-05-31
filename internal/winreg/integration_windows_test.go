//go:build windows

package winreg

import (
	"os"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestRegisterRoundTrip exercises the full HKCU key layout against the real
// registry. It is gated behind URLROUTER_REGISTRY_IT=1 so a normal `go test`
// never mutates browser-registration keys; run it explicitly to verify setup.
func TestRegisterRoundTrip(t *testing.T) {
	if os.Getenv("URLROUTER_REGISTRY_IT") != "1" {
		t.Skip("set URLROUTER_REGISTRY_IT=1 to run the registry integration test")
	}
	const fakeExe = `C:\Test\urlrouter.exe`
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
