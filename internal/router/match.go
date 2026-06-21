// Package router implements ROUTE mode: match a URL against the ordered rules
// and launch Chrome with the resolved profile (§5.3, §12).
package router

import (
	"log"
	"regexp"
	"strings"

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
// (§5.3, §5.4). source is the image name of the app that originated the click
// (e.g. "slack.exe"), resolved once per ROUTE invocation and injected so Match
// stays pure; "" means the source could not be determined. It is side-effect
// free apart from logging skipped rules, so both ROUTE mode and the editor/tray
// "test a URL" feature share it.
//
// Semantics, locked in by the spec:
//   - rules are evaluated top to bottom, first match wins;
//   - disabled rules are skipped;
//   - a rule with both a pattern and a source requires BOTH to match (AND); a
//     rule with only a source matches any URL from that app (§5.4);
//   - a fully blank rule (no pattern and no source) is skipped — an empty regex
//     matches every URL, so an unfinished rule must not silently capture all
//     routing (the editor warns about it too);
//   - patterns are unanchored RE2 (regexp.MatchString) and case-sensitive;
//   - source is a case-insensitive substring of the image name; an
//     undeterminable source ("") leaves a source predicate unsatisfied, so the
//     rule is skipped and matching fails open (§5.4);
//   - a pattern that fails to compile is logged and skipped — a broken rule
//     must never break routing;
//   - no match yields Matched=false, i.e. launch Chrome with no profile flag.
func Match(cfg *config.Config, url, source string) Result {
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
		if r.Pattern == "" && r.Source == "" {
			// A rule with neither predicate is inert: an empty regex matches
			// everything, so an unfinished rule must not hijack all routing.
			continue
		}
		// Pattern predicate. An empty pattern is a wildcard here only because a
		// non-empty Source makes the rule meaningful (source-only rule, §5.4);
		// the blank-and-sourceless case was already filtered out above.
		if r.Pattern != "" {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				log.Printf("skipping rule %s: bad pattern %q: %v", r.ID, r.Pattern, err)
				continue
			}
			if !re.MatchString(url) {
				continue
			}
		}
		// Source predicate (AND with the pattern). An undeterminable source fails
		// open: the predicate is simply unsatisfied, so the rule is skipped and
		// the next rule is tried (§5.4).
		if r.Source != "" && !matchesSource(r.Source, source) {
			continue
		}
		return Result{Matched: true, Rule: r, ProfileDirectory: r.ProfileDirectory}
	}
	return Result{}
}

// matchesSource reports whether a rule's configured source matches the
// originating app's image name (§5.4). The match is a case-insensitive
// substring so "slack" matches "Slack.exe" — friendlier for the canonical use
// case than an exact or glob match. An empty source never reaches here (the
// caller short-circuits), and an empty (undeterminable) origin can never satisfy
// a non-empty predicate, so it returns false → fail open.
func matchesSource(want, got string) bool {
	if got == "" {
		return false
	}
	return strings.Contains(strings.ToLower(got), strings.ToLower(want))
}
