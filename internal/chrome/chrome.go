// Package chrome discovers Chrome profiles and locates chrome.exe (§4).
package chrome

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Profile maps an on-disk profile directory to its friendly display name and the
// Google account signed into it. The editor shows Name; config and the launch
// flag use Directory (§4.1); Email/HostedDomain let a rule bind to a profile by
// account instead of by the brittle directory name (#22).
type Profile struct {
	Directory    string // e.g. "Default", "Profile 1".
	Name         string // friendly name shown in Chrome.
	Email        string // signed-in account email (Local State user_name); "" if signed out.
	HostedDomain string // Workspace hosted domain (hosted_domain); "" for consumer/Gmail accounts.
}

// localState mirrors the slice of Chrome's Local State we care about (§4.2).
type localState struct {
	Profile struct {
		InfoCache map[string]struct {
			Name         string `json:"name"`
			UserName     string `json:"user_name"`     // Account email, e.g. "joe@acme.com".
			HostedDomain string `json:"hosted_domain"` // Workspace domain, or the NO_HOSTED_DOMAIN sentinel.
		} `json:"info_cache"`
	} `json:"profile"`
}

// noHostedDomain is the sentinel Chrome stores in hosted_domain for consumer
// (non-Workspace) accounts; it is normalized to "" so only real Workspace
// domains surface on Profile.HostedDomain.
const noHostedDomain = "NO_HOSTED_DOMAIN"

// LocalStatePath returns the path to Chrome's Local State JSON file.
func LocalStatePath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "User Data", "Local State")
}

// Profiles reads Local State and returns the available profiles, ordered with
// "Default" first and then "Profile N" numerically. Re-read on editor open so
// newly-created profiles appear (§4.2).
func Profiles() ([]Profile, error) {
	data, err := os.ReadFile(LocalStatePath())
	if err != nil {
		return nil, fmt.Errorf("reading Local State: %w", err)
	}
	var ls localState
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, fmt.Errorf("parsing Local State: %w", err)
	}

	profiles := make([]Profile, 0, len(ls.Profile.InfoCache))
	for dir, info := range ls.Profile.InfoCache {
		hd := info.HostedDomain
		if hd == noHostedDomain {
			hd = ""
		}
		profiles = append(profiles, Profile{
			Directory:    dir,
			Name:         info.Name,
			Email:        info.UserName,
			HostedDomain: hd,
		})
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profileLess(profiles[i].Directory, profiles[j].Directory)
	})
	return profiles, nil
}

// profileLess orders "Default" first, then "Profile N" by numeric N, with any
// other names sorted lexically after.
func profileLess(a, b string) bool {
	ra, rb := profileRank(a), profileRank(b)
	if ra != rb {
		return ra < rb
	}
	return a < b
}

func profileRank(dir string) int {
	switch {
	case dir == "Default":
		return -1
	case strings.HasPrefix(dir, "Profile "):
		if n, err := strconv.Atoi(strings.TrimPrefix(dir, "Profile ")); err == nil {
			return n
		}
	}
	return 1 << 30 // Unknown names sort last.
}

// validProfileDir matches Chrome's on-disk profile directory names — "Default",
// "Profile 1", "Guest Profile", etc.: letters, digits, spaces, underscores, and
// hyphens only. Chrome never creates anything outside this set.
var validProfileDir = regexp.MustCompile(`^[A-Za-z0-9 _-]+$`)

// ValidProfileDir reports whether dir is a syntactically valid profile
// directory name. The router checks this before passing the value to
// chrome.exe, so a tampered config (§5.2) cannot push odd bytes into the
// command line even when Local State is unreadable and ProfileExists can't
// vet it against the real list.
func ValidProfileDir(dir string) bool {
	return validProfileDir.MatchString(dir)
}

// ProfileExists reports whether dir is a known Chrome profile directory. If
// Local State cannot be read it returns true: the router must not second-guess
// a configured profile just because discovery failed (§10 handles a genuinely
// missing profile by falling back to no flag).
func ProfileExists(dir string) bool {
	profiles, err := Profiles()
	if err != nil {
		return true
	}
	for _, p := range profiles {
		if p.Directory == dir {
			return true
		}
	}
	return false
}

// ResolveAccount maps a Google account — by email or Workspace hosted domain —
// to its current on-disk profile directory (§4.1, #22). email takes precedence:
// when non-empty it matches a profile whose account email (Local State
// user_name) equals it, case-insensitively. Otherwise hostedDomain matches a
// profile whose Workspace hosted domain — or, as a fallback when Chrome recorded
// none, the domain part of its email — equals it, case-insensitively. Profiles
// are searched in discovery order (Default, then Profile N), so the first
// matching profile wins when several share a domain.
//
// ok is false when nothing matches OR Local State cannot be read. An account
// that resolves to no current profile must fail closed to Chrome default (§10),
// unlike a literal profile_directory which is trusted when discovery fails —
// here the router has no name to fall back to, so an unreadable Local State
// drops to the default rather than guessing.
func ResolveAccount(email, hostedDomain string) (dir string, ok bool) {
	profiles, err := Profiles()
	if err != nil {
		return "", false
	}
	for _, p := range profiles {
		if email != "" {
			if strings.EqualFold(p.Email, email) {
				return p.Directory, true
			}
			continue
		}
		if hostedDomain != "" && profileHasDomain(p, hostedDomain) {
			return p.Directory, true
		}
	}
	return "", false
}

// profileHasDomain reports whether p belongs to the given hosted domain, matched
// case-insensitively against its recorded Workspace domain or, when Chrome left
// that blank, the part after "@" in its email.
func profileHasDomain(p Profile, domain string) bool {
	if p.HostedDomain != "" && strings.EqualFold(p.HostedDomain, domain) {
		return true
	}
	if at := strings.LastIndex(p.Email, "@"); at >= 0 {
		return strings.EqualFold(p.Email[at+1:], domain)
	}
	return false
}

// ResolvePath returns the path to chrome.exe using the resolution order in
// §4.3: explicit configured path, then the App Paths registry key (per-user
// then machine-wide), then common install fallbacks. The first existing path
// wins. It returns an error only when none of these resolve.
func ResolvePath(configured string) (string, error) {
	if configured != "" {
		if fileExists(configured) {
			return configured, nil
		}
		return "", fmt.Errorf("configured chrome_path does not exist: %s", configured)
	}

	if p := appPathsChrome(); p != "" && fileExists(p) {
		return p, nil
	}

	for _, p := range fallbackPaths() {
		if fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("chrome.exe not found (set chrome_path in config)")
}

// fallbackPaths lists common chrome.exe install locations (§4.3 item 3).
func fallbackPaths() []string {
	var paths []string
	for _, base := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LOCALAPPDATA"),
	} {
		if base != "" {
			paths = append(paths, filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
		}
	}
	return paths
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
