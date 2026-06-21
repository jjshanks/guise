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
	got := Match(c, "https://github.com/foo", "")
	if !got.Matched || got.ProfileDirectory != "Profile 3" || got.Rule.ID != "1" {
		t.Errorf("first match should win: %+v", got)
	}
}

func TestMatchNoMatchMeansNoFlag(t *testing.T) {
	c := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `github\.com/foo`, ProfileDirectory: "Profile 3"})
	got := Match(c, "https://github.com/bar", "")
	if got.Matched || got.ProfileDirectory != "" {
		t.Errorf("no rule should match → no flag: %+v", got)
	}
}

func TestMatchUnanchoredFootgun(t *testing.T) {
	// The documented foot-gun: github\.com/foo also catches github.com/foobar.
	c := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `github\.com/foo`, ProfileDirectory: "Profile 3"})
	if !Match(c, "https://github.com/foobar", "").Matched {
		t.Error("unanchored pattern should match foobar")
	}
	// Anchoring with a boundary pins it.
	c2 := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `github\.com/foo(/|$)`, ProfileDirectory: "Profile 3"})
	if Match(c2, "https://github.com/foobar", "").Matched {
		t.Error("anchored pattern should not match foobar")
	}
}

func TestMatchSkipsDisabled(t *testing.T) {
	c := cfg(
		config.Rule{ID: "1", Enabled: false, Pattern: `github\.com`, ProfileDirectory: "Profile 3"},
		config.Rule{ID: "2", Enabled: true, Pattern: `github\.com`, ProfileDirectory: "Profile 9"},
	)
	got := Match(c, "https://github.com/x", "")
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
	got := Match(c, "https://example.com", "")
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
	got := Match(c, "https://example.com", "")
	if !got.Matched || got.Rule.ID != "real" {
		t.Errorf("blank rule should be skipped, real rule should win: %+v", got)
	}
	// A blank rule alone yields no match → Chrome default.
	if Match(cfg(config.Rule{ID: "blank", Enabled: true, Pattern: ""}), "https://anything", "").Matched {
		t.Error("a lone blank rule should not match")
	}
}

func TestMatchCaseSensitiveByDefault(t *testing.T) {
	c := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `GitHub`, ProfileDirectory: "Profile 3"})
	if Match(c, "https://github.com", "").Matched {
		t.Error("matching should be case-sensitive by default")
	}
	c2 := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `(?i)GitHub`, ProfileDirectory: "Profile 3"})
	if !Match(c2, "https://github.com", "").Matched {
		t.Error("(?i) prefix should enable case-insensitive matching")
	}
}

func TestMatchNilConfig(t *testing.T) {
	// Match is exported and reuse-encouraged; a nil config must yield no match
	// rather than panic.
	if Match(nil, "https://github.com", "").Matched {
		t.Error("nil config should not match")
	}
}

func TestMatchSourceAndPatternIsAnd(t *testing.T) {
	// A rule with both a pattern and a source requires BOTH to match (§5.4).
	c := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `github\.com`, Source: "slack", ProfileDirectory: "Work"})

	// Pattern matches and source matches → win.
	if got := Match(c, "https://github.com/x", "Slack.exe"); !got.Matched || got.Rule.ID != "1" {
		t.Errorf("pattern+source both match should win: %+v", got)
	}
	// Pattern matches but source does not → skip.
	if Match(c, "https://github.com/x", "chrome.exe").Matched {
		t.Error("source mismatch should not match even when the pattern does")
	}
	// Source matches but pattern does not → skip.
	if Match(c, "https://example.com/x", "Slack.exe").Matched {
		t.Error("pattern mismatch should not match even when the source does")
	}
}

func TestMatchSourceOnlyMatchesAnyURL(t *testing.T) {
	// A rule with only a source (no pattern) matches any URL from that app (§5.4),
	// even though a blank pattern is otherwise inert.
	c := cfg(config.Rule{ID: "1", Enabled: true, Source: "slack", ProfileDirectory: "Work"})
	if got := Match(c, "https://anything.example/x", "Slack.exe"); !got.Matched || got.ProfileDirectory != "Work" {
		t.Errorf("source-only rule should match any URL from that app: %+v", got)
	}
	// ...but not a click from a different app.
	if Match(c, "https://anything.example/x", "chrome.exe").Matched {
		t.Error("source-only rule should not match a click from another app")
	}
}

func TestMatchUndeterminableSourceFailsOpen(t *testing.T) {
	// An undeterminable source ("") can never satisfy a source predicate, so the
	// rule is skipped and matching continues to a sourceless fallback rule (§5.4).
	c := cfg(
		config.Rule{ID: "src", Enabled: true, Pattern: `github\.com`, Source: "slack", ProfileDirectory: "Work"},
		config.Rule{ID: "any", Enabled: true, Pattern: `github\.com`, ProfileDirectory: "Personal"},
	)
	got := Match(c, "https://github.com/x", "")
	if !got.Matched || got.Rule.ID != "any" {
		t.Errorf("undeterminable source should skip the source rule and fall through: %+v", got)
	}
}

func TestMatchSourceCaseInsensitiveSubstring(t *testing.T) {
	// Source is a case-insensitive substring of the image name, so "slack" matches
	// "Slack.exe" and a partial fragment matches too.
	c := cfg(config.Rule{ID: "1", Enabled: true, Source: "SLACK", ProfileDirectory: "Work"})
	for _, src := range []string{"Slack.exe", "slack.exe", "myslackhelper.exe"} {
		if !Match(c, "https://x.example", src).Matched {
			t.Errorf("source %q should match the case-insensitive substring rule", src)
		}
	}
	if Match(c, "https://x.example", "teams.exe").Matched {
		t.Error("a non-matching source should not match")
	}
}

func TestMatchSourcelessRuleIgnoresSource(t *testing.T) {
	// A rule with no source is unaffected by the resolved source — it matches on
	// the pattern alone, so existing configs behave identically (§5.4).
	c := cfg(config.Rule{ID: "1", Enabled: true, Pattern: `github\.com`, ProfileDirectory: "Work"})
	if got := Match(c, "https://github.com/x", "slack.exe"); !got.Matched {
		t.Errorf("a sourceless rule should match regardless of source: %+v", got)
	}
	if got := Match(c, "https://github.com/x", ""); !got.Matched {
		t.Errorf("a sourceless rule should match with no source too: %+v", got)
	}
}

func TestMatchBlankRuleInertEvenWithSourceResolved(t *testing.T) {
	// A rule with neither a pattern nor a source stays inert even when a source is
	// resolved — an empty regex must never hijack all routing.
	c := cfg(
		config.Rule{ID: "blank", Enabled: true, ProfileDirectory: "Whoops"},
		config.Rule{ID: "real", Enabled: true, Pattern: `example\.com`, ProfileDirectory: "Real"},
	)
	got := Match(c, "https://example.com", "slack.exe")
	if !got.Matched || got.Rule.ID != "real" {
		t.Errorf("fully blank rule should stay inert: %+v", got)
	}
}
