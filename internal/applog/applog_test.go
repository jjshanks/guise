package applog

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupWritesToLogFile(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	f, err := Setup()
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	// Setup redirects the global logger; put it back so later tests in the
	// binary don't write to a closed file.
	defer log.SetOutput(os.Stderr)

	log.Print("hello from the test")
	if err := f.Close(); err != nil {
		t.Fatalf("closing log: %v", err)
	}

	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if !strings.Contains(string(data), "hello from the test") {
		t.Errorf("log file missing the test line; got %q", data)
	}
}

func TestRotateIfLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guise.log")

	// A missing file is a no-op.
	rotateIfLarge(path)
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Error("rotating a missing log should not create a .1")
	}

	// A small file stays put.
	if err := os.WriteFile(path, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	rotateIfLarge(path)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("small log was rotated away: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Error("small log should not produce a .1")
	}

	// An oversized file moves to .1, replacing any previous one.
	if err := os.WriteFile(path+".1", []byte("previous rotation"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("x"), maxLogSize)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	rotateIfLarge(path)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("oversized log not rotated; stat err = %v", err)
	}
	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("reading rotated log: %v", err)
	}
	if len(rotated) != maxLogSize {
		t.Errorf("rotated log is %d bytes, want %d (should replace the old .1)", len(rotated), maxLogSize)
	}
}
