//go:build windows

// Package winreg performs the HKCU-only registry work that makes guise
// eligible as the default browser (§3), detects whether it currently is the
// default (§3.3), and toggles login autostart (§7). Every write targets
// HKEY_CURRENT_USER, so no operation here ever needs administrator rights.
package winreg

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Identifiers shared across the registry layout (§3.1).
const (
	AppName        = "Guise"
	AppDescription = "Routes URLs to Chrome profiles by regex"
	progID         = "GuiseHTML"
	regAppKey      = "Guise" // Key name under RegisteredApplications and Run.

	clientKey         = `SOFTWARE\Clients\StartMenuInternet\Guise`
	capabilitiesKey   = clientKey + `\Capabilities`
	registeredAppsKey = `SOFTWARE\RegisteredApplications`
	classesKey        = `SOFTWARE\Classes`
	classesProgIDKey  = classesKey + `\` + progID
	runKey            = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`

	// assocBase is the per-scheme root Windows uses to record the chosen URL
	// handler (§3.3). Each scheme has a UserChoice and a newer, UCPD-protected
	// UserChoiceLatest beneath it. Windows 11 24H2+ resolves clicks through
	// UserChoiceLatest preferentially, so IsDefault reads both https keys (#9).
	assocBase           = `SOFTWARE\Microsoft\Windows\Shell\Associations\UrlAssociations\`
	userChoiceKey       = assocBase + `https\UserChoice`
	userChoiceLatestKey = assocBase + `https\UserChoiceLatest`
)

// command builds the shell open command: "<exe>" "%1". Windows substitutes the
// clicked URL into %1; quoting it preserves URLs containing &, spaces, etc.
func command(exe string) string {
	return `"` + exe + `" "%1"`
}

// Register writes all HKCU keys that make guise eligible as a default
// browser (§3.1). It is idempotent: re-running overwrites the same values,
// which is also how you update the recorded exe path after moving the binary.
func Register(exe string) error {
	writes := []struct {
		path  string
		name  string // "" = the key's (Default) value.
		value string
	}{
		{clientKey, "", AppName},
		{clientKey + `\DefaultIcon`, "", exe + ",0"},
		{capabilitiesKey, "ApplicationName", AppName},
		{capabilitiesKey, "ApplicationDescription", AppDescription},
		{capabilitiesKey + `\URLAssociations`, "http", progID},
		{capabilitiesKey + `\URLAssociations`, "https", progID},
		{clientKey + `\shell\open\command`, "", command(exe)},
		{registeredAppsKey, regAppKey, capabilitiesKey},
		{classesProgIDKey, "", "Guise Document"},
		{classesProgIDKey + `\shell\open\command`, "", command(exe)},
	}
	for _, w := range writes {
		if err := setString(w.path, w.name, w.value); err != nil {
			return fmt.Errorf("registering %s\\%s: %w", w.path, w.name, err)
		}
	}
	return nil
}

// Unregister removes the keys and values written by Register, leaving no trace
// in the registry. Missing keys are not an error — unregister is idempotent.
func Unregister() error {
	// Delete the RegisteredApplications value first so Windows immediately
	// stops listing us, then tear down the trees deepest-first.
	if err := deleteValue(registeredAppsKey, regAppKey); err != nil {
		return err
	}
	for _, path := range []string{
		clientKey + `\shell\open\command`,
		clientKey + `\shell\open`,
		clientKey + `\shell`,
		capabilitiesKey + `\URLAssociations`,
		capabilitiesKey,
		clientKey + `\DefaultIcon`,
		clientKey,
		classesProgIDKey + `\shell\open\command`,
		classesProgIDKey + `\shell\open`,
		classesProgIDKey + `\shell`,
		classesProgIDKey,
	} {
		if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}
	return nil
}

// IsDefault reports whether guise is the current https handler (§3.3, §3.4).
// Windows 11 24H2+ keeps two records per scheme — UserChoice and the newer,
// UCPD-protected UserChoiceLatest — and resolves clicks through UserChoiceLatest
// preferentially. Reading UserChoice alone gave a false healthy signal when
// UserChoiceLatest named a stale ProgID pointing at a deleted exe (#9), so we
// judge both: guise is default only if every populated key names a ProgID whose
// HKCU class command resolves to the current exe. We only ever read these values
// to detect state; the tamper-protected Hash means the default can never be
// forced here. A missing key simply means it does not constrain the verdict.
func IsDefault(exe string) (bool, error) {
	uc, err := readString(userChoiceKey, "ProgId")
	if err != nil {
		return false, fmt.Errorf("reading UserChoice ProgId: %w", err)
	}
	latest, err := readString(userChoiceLatestKey, "ProgId")
	if err != nil {
		return false, fmt.Errorf("reading UserChoiceLatest ProgId: %w", err)
	}
	return decideDefault(exe, uc, latest, handlerExe), nil
}

// decideDefault is the pure verdict behind IsDefault, split out from registry
// I/O so it is testable without HKCU (like repairProgIDs). resolve maps a ProgID
// to the exe its HKCU class command would launch (""=unresolvable). guise is
// default iff UserChoice resolves to exe and UserChoiceLatest, when present,
// also resolves to exe — so a stale ProgID in either slot fails the check.
func decideDefault(exe, ucProgID, latestProgID string, resolve func(string) string) bool {
	is := func(pid string) bool { return pid != "" && samePath(resolve(pid), exe) }
	if !is(ucProgID) {
		return false
	}
	return latestProgID == "" || is(latestProgID)
}

// handlerExe returns the exe that progID's HKCU class shell\open\command would
// launch, or "" if no such command exists or it cannot be parsed. It is the
// real resolver passed to decideDefault, and mirrors how repairProgIDs reads a
// ProgID's class command.
func handlerExe(progID string) string {
	cmd, err := readString(classesKey+`\`+progID+`\shell\open\command`, "")
	if err != nil || cmd == "" {
		return ""
	}
	return exeFromCommand(cmd)
}

// samePath reports whether two filesystem paths refer to the same file, modulo
// Windows path casing and separator/cleanup differences. An empty path never
// matches, so an unresolvable handler is never mistaken for the current exe.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// RepairStaleDefaults re-points stale ProgIDs left by earlier registrations of
// this tool (e.g. URLRouterHTML from before the urlrouter→guise rename) at the
// current exe (#8). Windows 11 keeps two handler records per scheme —
// UserChoice and the UCPD-protected UserChoiceLatest — and either can name a
// ProgID whose own shell\open\command points at a deleted binary, which makes
// every click fail with "Application not found" before guise is even invoked.
// Apps cannot write UserChoiceLatest, so the only self-service fix is to repair
// the ProgID's class (all HKCU) so it launches guise as an alias.
//
// It only touches ProgIDs that have an HKCU class command whose exe is missing,
// so system browsers (ChromeHTML, MSEdgeHTM — defined in HKLM) are never
// hijacked. Like routing, it fails soft: per-ProgID errors are logged and
// skipped, never returned. The slice of repaired ProgIDs is for the caller to
// log.
func RepairStaleDefaults(exe string) []string {
	seen := map[string]bool{}
	var candidates []string
	for _, scheme := range []string{"http", "https"} {
		for _, choice := range []string{"UserChoice", "UserChoiceLatest"} {
			pid := readProgID(assocBase + scheme + `\` + choice)
			// Skip the empty string, our own ProgID, and duplicates so each
			// candidate is examined once.
			if pid == "" || pid == progID || seen[pid] {
				continue
			}
			seen[pid] = true
			candidates = append(candidates, pid)
		}
	}
	return repairProgIDs(exe, candidates)
}

// repairProgIDs rewrites the shell\open\command of each candidate ProgID that
// has an HKCU class command whose exe is missing, pointing it at exe. ProgIDs
// without an HKCU class command (system-managed) or whose handler still exists
// are left untouched. Split out from RepairStaleDefaults so tests can drive it
// with throwaway ProgIDs without writing the real UserChoice keys.
func repairProgIDs(exe string, candidates []string) []string {
	var repaired []string
	for _, pid := range candidates {
		cmdKey := classesKey + `\` + pid + `\shell\open\command`
		cmd, err := readString(cmdKey, "")
		if err != nil {
			// A real registry error (e.g. broken ACL): log and skip rather than
			// mistake it for an absent key, but keep the pass alive.
			log.Printf("reading command for ProgID %q: %v", pid, err)
			continue
		}
		if cmd == "" {
			// No HKCU class command: not one of ours (system-managed). Leave it.
			continue
		}
		if handler := exeFromCommand(cmd); handler == "" || fileExists(handler) {
			continue // Still resolvable — nothing to repair.
		}
		if err := setString(cmdKey, "", command(exe)); err != nil {
			log.Printf("repair stale ProgID %q: %v", pid, err)
			continue
		}
		repaired = append(repaired, pid)
	}
	return repaired
}

// readProgID returns the ProgId value at a UserChoice/UserChoiceLatest key, or
// "" if the key or value is missing. A real read error is logged (and yields
// "") so callers treat it the same as "nothing to repair" without losing the
// diagnostic.
func readProgID(path string) string {
	pid, err := readString(path, "ProgId")
	if err != nil {
		log.Printf("reading ProgId at %s: %v", path, err)
	}
	return pid
}

// readString reads a string value (name "" = the (Default) value). A missing
// key or value returns ("", nil) — absence is not an error here; any other
// failure (broken ACL, wrong value type) is returned so the caller can log it.
func readString(path, name string) (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// exeFromCommand extracts the executable from a shell\open\command string. A
// quoted exe ("C:\path\app.exe" "%1") wins to the closing quote; otherwise the
// first whitespace-delimited token is taken. Any %VAR% is expanded so the path
// can be stat'd. Returns "" if no exe can be parsed.
func exeFromCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	var exe string
	if strings.HasPrefix(cmd, `"`) {
		if end := strings.IndexByte(cmd[1:], '"'); end >= 0 {
			exe = cmd[1 : 1+end]
		}
	} else if i := strings.IndexByte(cmd, ' '); i >= 0 {
		exe = cmd[:i]
	} else {
		exe = cmd
	}
	if expanded, err := registry.ExpandString(exe); err == nil {
		exe = expanded
	}
	return exe
}

// fileExists reports whether path resolves to an existing regular file. A
// leftover install *directory* (binary removed but folder intact) must still
// count as a stale handler, since the shell command can no longer launch it.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// SetAutostart toggles the login autostart Run value (§7). When enabled it
// writes the value "<exe>" --tray; when disabled it removes it. Only the tray
// autostarts — routing needs nothing resident.
func SetAutostart(enabled bool, exe string) error {
	if !enabled {
		return deleteValue(runKey, regAppKey)
	}
	return setString(runKey, regAppKey, `"`+exe+`" --tray`)
}

// IsAutostart reports whether the autostart Run value is present.
func IsAutostart() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false, fmt.Errorf("opening Run key: %w", err)
	}
	defer k.Close()

	_, _, err = k.GetStringValue(regAppKey)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading Run value: %w", err)
	}
	return true, nil
}

// setString creates the key (and any parents) and sets a string value. An
// empty name sets the key's (Default) value.
func setString(path, name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

// deleteValue removes a single named value, treating a missing key or value as
// success so callers stay idempotent.
func deleteValue(path, name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("deleting %s\\%s: %w", path, name, err)
	}
	return nil
}
