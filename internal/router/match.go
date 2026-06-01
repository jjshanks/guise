// Package router implements ROUTE mode: match a URL against the ordered rules
// and launch Chrome with the resolved profile (§5.3, §12).
package router

import (
	"log"
	"regexp"

	"guise/internal/config"
)

// Result describes the outcome of matching a URL against the rules.
type Result struct {
	// Matched is true when a rule won. When false, ProfileDirectory is empty
	// and the URL should launch with no --profile-directory flag.
	Matched bool
	// Rule is the winning rule (nil when Matched is false).
	Rule *config.Rule
	// ProfileDirectory is the directory name to launch, or "" for no flag.
	ProfileDirectory string
}

// Match evaluates url against cfg.Rules in order and returns the first match
// (§5.3). It is pure and side-effect free apart from logging skipped rules, so
// both ROUTE mode and the editor/tray "test a URL" feature share it.
//
// Semantics, locked in by the spec:
//   - rules are evaluated top to bottom, first match wins;
//   - disabled rules are skipped;
//   - a blank pattern is skipped — an empty regex matches every URL, so an
//     unfinished rule must not silently capture all routing (the editor warns
//     about it too);
//   - patterns are unanchored RE2 (regexp.MatchString) and case-sensitive;
//   - a pattern that fails to compile is logged and skipped — a broken rule
//     must never break routing;
//   - no match yields Matched=false, i.e. launch Chrome with no profile flag.
func Match(cfg *config.Config, url string) Result {
	if cfg == nil {
		// Defensive: an exported, reuse-encouraged function must not panic on a
		// nil config. No config means no rules, i.e. no match → Chrome default.
		return Result{}
	}
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if !r.Enabled {
			continue
		}
		if r.Pattern == "" {
			// regexp.Compile("") matches everything; treat a blank rule as inert
			// so an unfinished rule can't hijack all routing.
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			log.Printf("skipping rule %s: bad pattern %q: %v", r.ID, r.Pattern, err)
			continue
		}
		if re.MatchString(url) {
			return Result{Matched: true, Rule: r, ProfileDirectory: r.ProfileDirectory}
		}
	}
	return Result{}
}
