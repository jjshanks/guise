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

// Route is the heart of ROUTE mode (§12): load config, match the URL against
// the ordered rules, resolve chrome.exe, and launch it with the matched
// profile (or no profile flag on no match). It uses Start, not Run, so the
// process can exit immediately rather than lingering as Chrome's parent.
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

	res := Match(cfg, url)
	profileDir := res.ProfileDirectory
	// rule is the matched rule id, or "default" on no match. It is carried to the
	// single per-click line emitted after launch (§9: one line per click). The
	// match decision is not logged on its own line — the final routed/launch line
	// records which rule won, so the log stays one consolidated line per click.
	rule := "default"
	if res.Matched {
		rule = res.Rule.ID
	}

	// A profile must be syntactically valid and still exist; otherwise fall back
	// to Chrome's default (§10). The syntax check guards against a tampered
	// config injecting odd values into chrome.exe's command line even when Local
	// State is unreadable and ProfileExists cannot vet the name.
	if profileDir != "" && (!chrome.ValidProfileDir(profileDir) || !chrome.ProfileExists(profileDir)) {
		log.Printf("profile %q invalid or missing -> Chrome default", profileDir)
		profileDir = ""
	}

	chromePath, err := chrome.ResolvePath(cfg.ChromePath)
	if err != nil {
		log.Printf("cannot resolve chrome.exe: %v", err)
		notifyError("Guise", "Could not find chrome.exe.\n\nSet chrome_path in the config or install Chrome.")
		return fmt.Errorf("resolving chrome: %w", err)
	}

	args := launchArgs(profileDir, url)
	if err := startProcess(chromePath, args...); err != nil {
		log.Printf("launch failed url=%q rule=%q profile=%q chrome=%q: %v", url, rule, profileDir, chromePath, err)
		notifyError("Guise", "Failed to launch Chrome:\n"+err.Error())
		return fmt.Errorf("launching chrome: %w", err)
	}
	log.Printf("routed url=%q rule=%q profile=%q chrome=%q", url, rule, profileDir, chromePath)
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
