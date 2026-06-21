package chrome

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfilesParsesAndOrders(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	lsDir := filepath.Dir(LocalStatePath())
	if err := os.MkdirAll(lsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const body = `{"profile":{"info_cache":{
		"Profile 10":{"name":"Ten"},
		"Default":{"name":"Personal"},
		"Profile 2":{"name":"Work"}
	}}}`
	if err := os.WriteFile(LocalStatePath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Profiles()
	if err != nil {
		t.Fatalf("Profiles: %v", err)
	}
	wantOrder := []string{"Default", "Profile 2", "Profile 10"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d profiles, want %d", len(got), len(wantOrder))
	}
	for i, w := range wantOrder {
		if got[i].Directory != w {
			t.Errorf("position %d: got %q, want %q", i, got[i].Directory, w)
		}
	}
	if got[0].Name != "Personal" {
		t.Errorf("Default friendly name = %q, want Personal", got[0].Name)
	}
}

// writeLocalState points LOCALAPPDATA at a temp dir and writes the given Local
// State JSON there, returning nothing — the chrome package reads it via the
// LOCALAPPDATA-derived path.
func writeLocalState(t *testing.T, body string) {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(LocalStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LocalStatePath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// accountFixture mixes a Workspace account, a consumer Gmail account (with
// Chrome's NO_HOSTED_DOMAIN sentinel), and a signed-out profile.
const accountFixture = `{"profile":{"info_cache":{
	"Default":   {"name":"Personal","user_name":"me@gmail.com","hosted_domain":"NO_HOSTED_DOMAIN"},
	"Profile 1": {"name":"Work","user_name":"joe@acme.com","hosted_domain":"acme.com"},
	"Profile 2": {"name":"Side"}
}}}`

func TestProfilesExposesAccounts(t *testing.T) {
	writeLocalState(t, accountFixture)
	got, err := Profiles()
	if err != nil {
		t.Fatalf("Profiles: %v", err)
	}
	by := map[string]Profile{}
	for _, p := range got {
		by[p.Directory] = p
	}
	if p := by["Profile 1"]; p.Email != "joe@acme.com" || p.HostedDomain != "acme.com" {
		t.Errorf("Workspace profile = %+v, want joe@acme.com / acme.com", p)
	}
	// The NO_HOSTED_DOMAIN sentinel normalizes to "" so only real domains surface.
	if p := by["Default"]; p.Email != "me@gmail.com" || p.HostedDomain != "" {
		t.Errorf("Gmail profile = %+v, want me@gmail.com / empty hosted domain", p)
	}
	if p := by["Profile 2"]; p.Email != "" || p.HostedDomain != "" {
		t.Errorf("signed-out profile = %+v, want empty account fields", p)
	}
}

func TestResolveAccount(t *testing.T) {
	writeLocalState(t, accountFixture)
	tests := []struct {
		name         string
		email        string
		hostedDomain string
		wantDir      string
		wantOK       bool
	}{
		{"email exact", "joe@acme.com", "", "Profile 1", true},
		{"email case-insensitive", "JOE@ACME.COM", "", "Profile 1", true},
		{"hosted domain", "", "acme.com", "Profile 1", true},
		{"hosted domain case-insensitive", "", "ACME.COM", "Profile 1", true},
		{"email beats domain when both set", "me@gmail.com", "acme.com", "Default", true},
		// Gmail has no hosted_domain field, but the domain is recoverable from the
		// email so a hosted-domain rule still resolves it.
		{"domain falls back to email domain", "", "gmail.com", "Default", true},
		{"unknown email", "nobody@acme.com", "", "", false},
		{"unknown domain", "", "other.com", "", false},
		{"empty selects nothing", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, ok := ResolveAccount(tt.email, tt.hostedDomain)
			if dir != tt.wantDir || ok != tt.wantOK {
				t.Errorf("ResolveAccount(%q,%q) = %q,%v; want %q,%v", tt.email, tt.hostedDomain, dir, ok, tt.wantDir, tt.wantOK)
			}
		})
	}
}

func TestResolveAccountUnreadableFailsClosed(t *testing.T) {
	// No Local State on disk: an account that cannot be resolved must report
	// ok=false so the router fails closed to Chrome default (§10), unlike a literal
	// profile_directory which is trusted when discovery fails.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if _, ok := ResolveAccount("joe@acme.com", ""); ok {
		t.Error("unreadable Local State should fail closed (ok=false)")
	}
}

func TestValidProfileDir(t *testing.T) {
	tests := []struct {
		dir  string
		want bool
	}{
		{"Default", true},
		{"Profile 1", true},
		{"Guest Profile", true},
		{"My-Profile_2", true},
		{"", false},
		{`Profile 3" --user-data-dir=C:\evil`, false},
		{"Profile\n1", false},
		{"a/b", false},
		{"a\\b", false},
	}
	for _, tt := range tests {
		if got := ValidProfileDir(tt.dir); got != tt.want {
			t.Errorf("ValidProfileDir(%q) = %v, want %v", tt.dir, got, tt.want)
		}
	}
}

func TestResolvePathConfiguredExisting(t *testing.T) {
	f := filepath.Join(t.TempDir(), "chrome.exe")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolvePath(f)
	if err != nil || got != f {
		t.Errorf("ResolvePath(%q) = %q, %v; want the path", f, got, err)
	}
}

func TestResolvePathConfiguredMissing(t *testing.T) {
	_, err := ResolvePath(filepath.Join(t.TempDir(), "nope.exe"))
	if err == nil {
		t.Error("expected error for non-existent configured path")
	}
}
