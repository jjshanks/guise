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
	// UserChoiceLatest beneath it; userChoiceKey is the https UserChoice that
	// IsDefault reads.
	assocBase     = `SOFTWARE\Microsoft\Windows\Shell\Associations\UrlAssociations\`
	userChoiceKey = assocBase + `https\UserChoice`
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

// IsDefault reports whether guise is the current https handler, by reading
// the UserChoice ProgId (§3.3). We only ever read this value to detect state;
// the tamper-protected Hash means it can never be written here to force the
// default. A missing key simply means we are not the default.
func IsDefault() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, userChoiceKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("opening UserChoice: %w", err)
	}
	defer k.Close()

	got, _, err := k.GetStringValue("ProgId")
	if err != nil {
		return false, fmt.Errorf("reading ProgId: %w", err)
	}
	return got == progID, nil
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
		cmd, ok := readString(cmdKey, "")
		if !ok {
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
// "" if the key or value is missing or unreadable — callers treat absence the
// same as "nothing to repair".
func readProgID(path string) string {
	pid, _ := readString(path, "ProgId")
	return pid
}

// readString reads a string value (name "" = the (Default) value), reporting
// ok=false for any missing key/value or read error.
func readString(path, name string) (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return "", false
	}
	return v, true
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

// fileExists reports whether path resolves to an existing filesystem entry.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
