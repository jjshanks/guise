package router

import (
	"testing"

	"guise/internal/config"
)

func cfg(rules ...config.Rule) *config.Config {
	return &config.Config{Version: 1, Rules: rules}
}

func TestMatchFirstWins(t *testing.T) {
	c := cfg(
		config.Rule{ID: "1", Enabled: true, Pattern: `github\.com/foo`, ProfileDirectory: "Profile 3"},
		config.Rule{ID: "2", Enabled: true, Pattern: `github\.com`, ProfileDirectory: "Profile 9"},
	)
	got := Match(c, "https://github.com/foo")
	if !got.Matched || got.ProfileDirectory != "Profile 3" || got.Rule.ID != "1" {
		t.Errorf("first match should win: %+v", got)
	}
}

func TestMatchNoMatchMeansNoFlag(t *testing.T) {
	c := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `github\.com/foo`, ProfileDirectory: "Profile 3"})
	got := Match(c, "https://github.com/bar")
	if got.Matched || got.ProfileDirectory != "" {
		t.Errorf("no rule should match → no flag: %+v", got)
	}
}

func TestMatchUnanchoredFootgun(t *testing.T) {
	// The documented foot-gun: github\.com/foo also catches github.com/foobar.
	c := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `github\.com/foo`, ProfileDirectory: "Profile 3"})
	if !Match(c, "https://github.com/foobar").Matched {
		t.Error("unanchored pattern should match foobar")
	}
	// Anchoring with a boundary pins it.
	c2 := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `github\.com/foo(/|$)`, ProfileDirectory: "Profile 3"})
	if Match(c2, "https://github.com/foobar").Matched {
		t.Error("anchored pattern should not match foobar")
	}
}

func TestMatchSkipsDisabled(t *testing.T) {
	c := cfg(
		config.Rule{ID: "1", Enabled: false, Pattern: `github\.com`, ProfileDirectory: "Profile 3"},
		config.Rule{ID: "2", Enabled: true, Pattern: `github\.com`, ProfileDirectory: "Profile 9"},
	)
	got := Match(c, "https://github.com/x")
	if got.Rule.ID != "2" {
		t.Errorf("disabled rule should be skipped: %+v", got)
	}
}

func TestMatchSkipsBrokenPattern(t *testing.T) {
	// A rule that fails to compile must not break routing; the next one wins.
	c := cfg(
		config.Rule{ID: "bad", Enabled: true, Pattern: `(unterminated`, ProfileDirectory: "Profile 3"},
		config.Rule{ID: "good", Enabled: true, Pattern: `example\.com`, ProfileDirectory: "Profile 1"},
	)
	got := Match(c, "https://example.com")
	if !got.Matched || got.Rule.ID != "good" {
		t.Errorf("broken pattern should be skipped: %+v", got)
	}
}

func TestMatchSkipsEmptyPattern(t *testing.T) {
	// An empty regex matches every URL; a blank (unfinished) rule must not
	// capture all routing and short-circuit the rules below it.
	c := cfg(
		config.Rule{ID: "blank", Enabled: true, Pattern: "", ProfileDirectory: "Profile 9"},
		config.Rule{ID: "real", Enabled: true, Pattern: `example\.com`, ProfileDirectory: "Profile 1"},
	)
	got := Match(c, "https://example.com")
	if !got.Matched || got.Rule.ID != "real" {
		t.Errorf("blank rule should be skipped, real rule should win: %+v", got)
	}
	// A blank rule alone yields no match → Chrome default.
	if Match(cfg(config.Rule{ID: "blank", Enabled: true, Pattern: ""}), "https://anything").Matched {
		t.Error("a lone blank rule should not match")
	}
}

func TestMatchCaseSensitiveByDefault(t *testing.T) {
	c := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `GitHub`, ProfileDirectory: "Profile 3"})
	if Match(c, "https://github.com").Matched {
		t.Error("matching should be case-sensitive by default")
	}
	c2 := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `(?i)GitHub`, ProfileDirectory: "Profile 3"})
	if !Match(c2, "https://github.com").Matched {
		t.Error("(?i) prefix should enable case-insensitive matching")
	}
}

func TestMatchNilConfig(t *testing.T) {
	// Match is exported and reuse-encouraged; a nil config must yield no match
	// rather than panic.
	if Match(nil, "https://github.com").Matched {
		t.Error("nil config should not match")
	}
}
