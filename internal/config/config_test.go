package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load on missing config: %v", err)
	}
	if cfg.Version != SchemaVersion {
		t.Errorf("version = %d, want %d", cfg.Version, SchemaVersion)
	}
	if len(cfg.Rules) != 0 {
		t.Errorf("rules = %d, want 0", len(cfg.Rules))
	}
}

func TestLoadMalformedReturnsDefaultAndError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err == nil {
		t.Fatal("expected error on malformed config")
	}
	if cfg == nil || len(cfg.Rules) != 0 {
		t.Errorf("expected non-nil default config on malformed input, got %+v", cfg)
	}
}

func TestSaveLoadRoundTripAtomic(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	want := &Config{
		Version:    SchemaVersion,
		ChromePath: `C:\chrome.exe`,
		Rules: []Rule{
			{ID: "a", Enabled: true, Pattern: `github\.com/foo`, ProfileDirectory: "Profile 3", Comment: "gh"},
			{ID: "b", Enabled: false, Pattern: `mail\.google\.com`, ProfileDirectory: "Profile 1"},
		},
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// No temp files left behind after an atomic save.
	entries, _ := os.ReadDir(Dir())
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || filepath.Base(e.Name()) != "config.json" {
			t.Errorf("leftover file after save: %s", e.Name())
		}
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("round trip mismatch:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}
