package router

import (
	"fmt"
	"log"
	"os/exec"

	"guise/internal/chrome"
	"guise/internal/config"
	"guise/internal/notify"
)

// Seams for testing. startProcess launches Chrome detached (Start, not Run, so
// ROUTE mode exits immediately rather than lingering as Chrome's parent);
// notifyError pops a Windows message box, which blocks until dismissed. Tests
// override both so the launch decision can be asserted without spawning Chrome
// or popping a modal dialog.
var (
	startProcess = func(path string, args ...string) error {
		return exec.Command(path, args...).Start()
	}
	notifyError = notify.Error
)

// Resolution is the outcome of running a URL through the full ROUTE pipeline
// (§15) without launching: apply pre-rewrites, match the rules, validate and
// fall back the profile, then apply delayed rewrites. It is the single source of
// truth shared by ROUTE mode and the editor's "Test URL" preview, so the preview
// can never drift from a real click.
type Resolution struct {
	Original         string       // The URL as clicked or typed.
	URL              string       // The final URL after all rewrites — what Chrome opens.
	Rule             *config.Rule // The matched rule, or nil on no match.
	ProfileDirectory string       // Profile dir after fallback ("" = Chrome default).
	ProfileDropped   bool         // True when a matched profile was invalid/missing and fell back.
	Applied          []string     // IDs of the rewrites that changed the URL, in order.
}

// Resolve runs the ROUTE pipeline against url without launching Chrome: apply
// non-delayed rewrites, match the ordered rules, drop a syntactically invalid or
// vanished profile to Chrome default (§10), then apply delayed rewrites. It reads
// Chrome's Local State to vet the profile (same as Route) but has no other side
// effects, so the editor preview can call it directly.
func Resolve(cfg *config.Config, url string) Resolution {
	// Pre-rewrites (§15) run before profile selection, so both the match and the
	// launched URL see the rewritten string. A nil/empty rewrite list is a no-op,
	// so configs without rewrites resolve exactly as before.
	original := url
	url, preApplied := ApplyRewrites(cfg.Rewrites, url, false)

	res := Match(cfg, url)
	profileDir := res.ProfileDirectory

	// A profile must be syntactically valid and still exist; otherwise fall back
	// to Chrome's default (§10). The syntax check guards against a tampered config
	// injecting odd values into chrome.exe's command line even when Local State is
	// unreadable and ProfileExists cannot vet the name.
	dropped := false
	if profileDir != "" && (!chrome.ValidProfileDir(profileDir) || !chrome.ProfileExists(profileDir)) {
		profileDir = ""
		dropped = true
	}

	// Delayed rewrites (§15) run after the profile is chosen, so a transform can
	// change the launched URL without affecting which profile it routes to.
	url, postApplied := ApplyRewrites(cfg.Rewrites, url, true)

	return Resolution{
		Original:         original,
		URL:              url,
		Rule:             res.Rule,
		ProfileDirectory: profileDir,
		ProfileDropped:   dropped,
		Applied:          append(preApplied, postApplied...),
	}
}

// Route is the heart of ROUTE mode (§12): load config, resolve the URL through
// the rewrite/match pipeline (Resolve), resolve chrome.exe, and launch it with
// the matched profile (or no profile flag on no match). It uses Start, not Run,
// so the process can exit immediately rather than lingering as Chrome's parent.
//
// Routing is deliberately hard to break: a malformed config still routes
// everything to Chrome's default, a vanished profile falls back to no flag,
// and only a genuinely unresolvable chrome.exe stops it (with a notification).
func Route(url string) error {
	cfg, err := config.Load()
	if err != nil {
		// Bad config must never block routing (§10); proceed with the default,
		// which routes every URL to Chrome's own behavior.
		log.Printf("config error, routing to Chrome default: %v", err)
	}

	r := Resolve(cfg, url)
	// rule is the matched rule id, or "default" on no match. It is carried to the
	// single per-click line emitted after launch (§9: one line per click). The
	// match decision is not logged on its own line — the final routed/launch line
	// records which rule won, so the log stays one consolidated line per click.
	rule := "default"
	if r.Rule != nil {
		rule = r.Rule.ID
	}
	if r.ProfileDropped {
		// r.Rule is non-nil whenever a profile was dropped (a profile can only come
		// from a matched rule), so logging its configured directory is safe.
		log.Printf("profile %q invalid or missing -> Chrome default", r.Rule.ProfileDirectory)
	}

	chromePath, err := chrome.ResolvePath(cfg.ChromePath)
	if err != nil {
		log.Printf("cannot resolve chrome.exe: %v", err)
		notifyError("Guise", "Could not find chrome.exe.\n\nSet chrome_path in the config or install Chrome.")
		return fmt.Errorf("resolving chrome: %w", err)
	}

	args := launchArgs(r.ProfileDirectory, r.URL)
	if err := startProcess(chromePath, args...); err != nil {
		log.Printf("launch failed url=%q final=%q rule=%q profile=%q rewrites=%v chrome=%q: %v", r.Original, r.URL, rule, r.ProfileDirectory, r.Applied, chromePath, err)
		notifyError("Guise", "Failed to launch Chrome:\n"+err.Error())
		return fmt.Errorf("launching chrome: %w", err)
	}
	// One consolidated line per click (§9). final= and rewrites= are included so a
	// URL the rewrites changed is debuggable; for the common no-rewrite case final
	// equals url and rewrites is empty.
	log.Printf("routed url=%q final=%q rule=%q profile=%q rewrites=%v chrome=%q", r.Original, r.URL, rule, r.ProfileDirectory, r.Applied, chromePath)
	return nil
}

// launchArgs builds the chrome.exe argument list. An empty profileDir omits
// the --profile-directory flag entirely (no-match → Chrome default), and an
// empty url launches Chrome with no URL (§10: no-argument ROUTE mode). The URL
// is always passed as a distinct argv entry, never a shell string, so weird
// URLs survive without quoting injection (§10).
func launchArgs(profileDir, url string) []string {
	var args []string
	if profileDir != "" {
		args = append(args, "--profile-directory="+profileDir)
	}
	if url != "" {
		args = append(args, url)
	}
	return args
}
