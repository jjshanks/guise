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
