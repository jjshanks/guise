package router

import (
	"strings"

	"guise/internal/config"
)

// ApplyRewrites returns url with every enabled rewrite whose Delayed flag equals
// `delayed` applied in order via literal string replacement (§15). Each rewrite
// operates on the output of the previous one, so transforms chain. It also
// returns the IDs of the rewrites that actually changed the URL, for the
// per-click log line.
//
// Like routing, rewriting never fails closed:
//   - a disabled rewrite is skipped;
//   - a blank Find is skipped — strings.ReplaceAll with an empty search string
//     splices Replace between every character, so an unfinished rewrite must not
//     silently mangle every URL (the editor warns about it too);
//   - a Find that does not occur is a harmless no-op and is not reported as
//     applied.
//
// It is pure and side-effect free, so ROUTE mode and the editor's "test URL"
// preview share it.
func ApplyRewrites(rewrites []config.Rewrite, url string, delayed bool) (string, []string) {
	var applied []string
	for i := range rewrites {
		rw := &rewrites[i]
		if !rw.Enabled || rw.Delayed != delayed || rw.Find == "" {
			continue
		}
		next := strings.ReplaceAll(url, rw.Find, rw.Replace)
		if next != url {
			applied = append(applied, rw.ID)
			url = next
		}
	}
	return url, applied
}
