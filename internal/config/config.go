// Package config defines the urlrouter on-disk configuration (§5) and the
// routines to load and atomically save it. The schema and matching order here
// are the behavioral contract the whole app orbits around.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Rule is a single ordered routing rule. Rules are evaluated top to bottom,
// first match wins (§5.3).
type Rule struct {
	ID               string `json:"id"`
	Enabled          bool   `json:"enabled"`
	Pattern          string `json:"pattern"`           // Go RE2 pattern, matched unanchored.
	ProfileDirectory string `json:"profile_directory"` // On-disk dir name, e.g. "Profile 3".
	Comment          string `json:"comment"`
}

// Config is the full configuration document (§5.2).
type Config struct {
	Version    int    `json:"version"`
	ChromePath string `json:"chrome_path"` // Empty = auto-detect (§4.3).
	Rules      []Rule `json:"rules"`
}

// SchemaVersion is the current config schema version.
const SchemaVersion = 1

// Dir returns the configuration directory, %APPDATA%\URLRouter (§5.1).
func Dir() string {
	return filepath.Join(os.Getenv("APPDATA"), "URLRouter")
}

// Path returns the full path to config.json.
func Path() string {
	return filepath.Join(Dir(), "config.json")
}

// Default returns an empty but valid configuration. With no rules, every URL
// routes to Chrome's default behavior (no --profile-directory flag).
func Default() *Config {
	return &Config{Version: SchemaVersion, Rules: []Rule{}}
}

// Load reads and parses config.json. A missing file is not an error: it yields
// the default config so a fresh install routes everything to Chrome's default.
// A malformed file returns a non-nil error AND the default config, so callers
// in ROUTE mode can keep routing (§10) while surfacing the problem.
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Rules == nil {
		cfg.Rules = []Rule{}
	}
	return &cfg, nil
}

// Save writes cfg to config.json atomically: it writes a temp file in the same
// directory and renames it over the target (§6.2), so a crash mid-write can
// never leave a half-written config.
func Save(cfg *Config) error {
	if cfg.Version == 0 {
		cfg.Version = SchemaVersion
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "config-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // No-op once the rename succeeds.

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp config: %w", err)
	}
	if err := os.Rename(tmpName, Path()); err != nil {
		return fmt.Errorf("replacing config: %w", err)
	}
	return nil
}
