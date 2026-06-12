// Package config defines the guise on-disk configuration (§5) and the
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

// Rewrite is a literal find-and-replace transform applied to the URL before it
// is launched (§15). Unlike Rules, rewrites are not first-match-wins: every
// enabled rewrite is applied in order, each operating on the output of the one
// before, so transforms chain (e.g. swap the host, then strip a query param).
//
// Timing is governed by Delayed:
//   - Delayed == false (the default): the rewrite runs BEFORE profile selection,
//     so both the profile match and the launched URL see the rewritten string.
//     This is the common case — e.g. x.com → xcancel.com, then route as usual.
//   - Delayed == true: the rewrite runs AFTER profile matching, so the profile is
//     chosen from the original URL but Chrome launches the rewritten URL. Use this
//     when the rewrite would otherwise change which profile a URL routes to.
//
// Find/Replace are literal substrings (no regex in the MVP); every occurrence of
// Find is replaced. A blank Find is inert (it would otherwise splice Replace
// between every character), mirroring how a blank rule Pattern is skipped.
type Rewrite struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Find    string `json:"find"`              // Literal substring to match (no regex in the MVP).
	Replace string `json:"replace"`           // Literal replacement for every occurrence of Find.
	Delayed bool   `json:"delayed,omitempty"` // Apply after profile matching instead of before.
	Comment string `json:"comment"`
}

// Config is the full configuration document (§5.2).
type Config struct {
	Version    int    `json:"version"`
	ChromePath string `json:"chrome_path"` // Empty = auto-detect (§4.3).
	// AutoUpdate toggles the tray's background check for new GitHub releases
	// (§14). A pointer so an absent field (older configs, fresh installs) is
	// distinguishable from an explicit false: nil means enabled, so auto-update
	// is on by default without having to rewrite existing configs.
	AutoUpdate *bool  `json:"auto_update,omitempty"`
	Rules      []Rule `json:"rules"`
	// Rewrites are literal URL find-and-replace transforms (§15). Omitted from
	// JSON when empty so existing configs (and the round-trip) are unaffected
	// until a rewrite is actually added.
	Rewrites []Rewrite `json:"rewrites,omitempty"`
}

// AutoUpdateEnabled reports whether the tray should check for new releases in
// the background. An absent (nil) value counts as enabled (§14).
func (c *Config) AutoUpdateEnabled() bool {
	return c.AutoUpdate == nil || *c.AutoUpdate
}

// SetAutoUpdate records an explicit on/off choice from the tray toggle so it
// persists across restarts.
func (c *Config) SetAutoUpdate(on bool) { c.AutoUpdate = &on }

// SchemaVersion is the current config schema version.
const SchemaVersion = 1

// Dir returns the configuration directory, %APPDATA%\Guise (§5.1).
func Dir() string {
	return filepath.Join(os.Getenv("APPDATA"), "Guise")
}

// Path returns the full path to config.json.
func Path() string {
	return filepath.Join(Dir(), "config.json")
}

// Default returns an empty but valid configuration. With no rules, every URL
// routes to Chrome's default behavior (no --profile-directory flag). Both slices
// are non-nil so a fresh install (missing config) matches a loaded one — Load
// normalizes the same way — and callers never see nil.
func Default() *Config {
	return &Config{Version: SchemaVersion, Rules: []Rule{}, Rewrites: []Rewrite{}}
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
	if cfg.Rewrites == nil {
		cfg.Rewrites = []Rewrite{}
	}
	return &cfg, nil
}

// Save writes cfg to config.json atomically: it writes a temp file in the same
// directory and renames it over the target (§6.2), so a crash mid-write can
// never leave a half-written config.
func Save(cfg *Config) error {
	// Default the version on a copy so Save has no observable side effect on the
	// caller's struct (the Rules slice is shared but never mutated here).
	out := *cfg
	if out.Version == 0 {
		out.Version = SchemaVersion
	}
	data, err := json.MarshalIndent(&out, "", "  ")
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
