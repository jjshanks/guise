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

// TestDecideDefault drives the pure default-browser verdict (#9) without the
// registry, using a fake resolver mapping ProgID→handler exe. It is the verdict
// IsDefault delegates to once it has read the two https keys.
func TestDecideDefault(t *testing.T) {
	const exe = `C:\Apps\Guise\guise.exe`
	// resolve mimics handlerExe: GuiseHTML and a repaired alias launch the
	// current exe; ChromeHTML launches Chrome; the stale alias launches a
	// since-deleted binary; an unknown ProgID resolves to nothing.
	resolve := func(pid string) string {
		switch pid {
		case "GuiseHTML", "URLRouterHTML-repaired":
			return exe
		case "ChromeHTML":
			return `C:\Program Files\Google\Chrome\Application\chrome.exe`
		case "URLRouterHTML-stale":
			return `C:\Old\urlrouter\urlrouter.exe` // deleted; handlerExe would still parse it
		default:
			return ""
		}
	}
	tests := []struct {
		name   string
		uc     string
		latest string
		want   bool
	}{
		{"both empty", "", "", false},
		{"guise, no latest (older win11)", "GuiseHTML", "", true},
		{"guise + stale latest (bug, pre-repair)", "GuiseHTML", "URLRouterHTML-stale", false},
		{"guise + repaired alias latest (post #8 repair)", "GuiseHTML", "URLRouterHTML-repaired", true},
		{"chrome chosen", "ChromeHTML", "ChromeHTML", false},
		{"userchoice empty, latest guise", "", "GuiseHTML", false},
		{"guise + unresolvable latest", "GuiseHTML", "GhostHTML", false},
		{"repaired alias in both slots", "URLRouterHTML-repaired", "URLRouterHTML-repaired", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideDefault(exe, tt.uc, tt.latest, resolve); got != tt.want {
				t.Errorf("decideDefault(uc=%q, latest=%q) = %v, want %v", tt.uc, tt.latest, got, tt.want)
			}
		})
	}
}

func TestSamePath(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", `C:\A\guise.exe`, `C:\A\guise.exe`, true},
		{"case-insensitive", `C:\A\Guise.exe`, `c:\a\guise.EXE`, true},
		{"separators and dot segments", `C:\A\.\guise.exe`, `C:\A\guise.exe`, true},
		{"different file", `C:\A\guise.exe`, `C:\A\chrome.exe`, false},
		{"empty never matches", ``, `C:\A\guise.exe`, false},
		{"both empty", ``, ``, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := samePath(tt.a, tt.b); got != tt.want {
				t.Errorf("samePath(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
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
