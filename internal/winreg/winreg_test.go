//go:build windows

package winreg

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestCommandQuoting(t *testing.T) {
	got := command(`C:\Program Files\URLRouter\urlrouter.exe`)
	want := `"C:\Program Files\URLRouter\urlrouter.exe" "%1"`
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

// TestSetAndDeleteValue exercises the registry plumbing against a throwaway key
// so it never disturbs the real browser-registration or autostart entries.
func TestSetAndDeleteValue(t *testing.T) {
	const testKey = `SOFTWARE\URLRouterSelfTest`
	t.Cleanup(func() { registry.DeleteKey(registry.CURRENT_USER, testKey) })

	if err := setString(testKey, "val", "hello"); err != nil {
		t.Fatalf("setString: %v", err)
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, testKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, _, err := k.GetStringValue("val")
	k.Close()
	if err != nil || got != "hello" {
		t.Fatalf("GetStringValue = %q, %v; want hello", got, err)
	}

	if err := deleteValue(testKey, "val"); err != nil {
		t.Fatalf("deleteValue: %v", err)
	}
	// Deleting a missing value is a no-op, not an error.
	if err := deleteValue(testKey, "val"); err != nil {
		t.Errorf("deleteValue on missing value should be nil, got %v", err)
	}
}
