package router

import (
	"fmt"
	"log"
	"os/exec"

	"guise/internal/chrome"
	"guise/internal/config"
	"guise/internal/notify"
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
	if res.Matched {
		log.Printf("matched rule %s -> profile %q for %q", res.Rule.ID, profileDir, url)
	} else {
		log.Printf("no rule matched %q -> Chrome default", url)
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
		notify.Error("Guise", "Could not find chrome.exe.\n\nSet chrome_path in the config or install Chrome.")
		return fmt.Errorf("resolving chrome: %w", err)
	}

	args := launchArgs(profileDir, url)
	if err := exec.Command(chromePath, args...).Start(); err != nil {
		log.Printf("launch failed chrome=%q args=%v: %v", chromePath, args, err)
		notify.Error("Guise", "Failed to launch Chrome:\n"+err.Error())
		return fmt.Errorf("launching chrome: %w", err)
	}
	log.Printf("launched chrome=%q profile=%q url=%q", chromePath, profileDir, url)
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
