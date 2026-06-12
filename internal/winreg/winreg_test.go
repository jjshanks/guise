//go:build windows

package winreg

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestCommandQuoting(t *testing.T) {
	got := command(`C:\Program Files\Guise\guise.exe`)
	want := `"C:\Program Files\Guise\guise.exe" "%1"`
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestExeFromCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"quoted with arg", `"C:\Program Files\App\app.exe" "%1"`, `C:\Program Files\App\app.exe`},
		{"unquoted with arg", `C:\Apps\app.exe "%1"`, `C:\Apps\app.exe`},
		{"bare exe", `C:\Apps\app.exe`, `C:\Apps\app.exe`},
		{"leading space", `  "C:\Apps\app.exe" "%1"`, `C:\Apps\app.exe`},
		{"empty", ``, ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exeFromCommand(tt.cmd); got != tt.want {
				t.Errorf("exeFromCommand(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}

	// An env-var-prefixed path is expanded so it can be stat'd. %SystemRoot% is
	// always set on Windows, so the result must start with its expansion.
	if got := exeFromCommand(`%SystemRoot%\system32\app.exe`); !strings.Contains(got, `\system32\app.exe`) || strings.Contains(got, "%") {
		t.Errorf("exeFromCommand env expansion = %q, want %%SystemRoot%% expanded", got)
	}
}

// TestSetAndDeleteValue exercises the registry plumbing against a throwaway key
// so it never disturbs the real browser-registration or autostart entries.
func TestSetAndDeleteValue(t *testing.T) {
	const testKey = `SOFTWARE\GuiseSelfTest`
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
