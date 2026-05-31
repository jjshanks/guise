//go:build windows

// Package winreg performs the HKCU-only registry work that makes urlrouter
// eligible as the default browser (§3), detects whether it currently is the
// default (§3.3), and toggles login autostart (§7). Every write targets
// HKEY_CURRENT_USER, so no operation here ever needs administrator rights.
package winreg

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// Identifiers shared across the registry layout (§3.1).
const (
	AppName        = "URL Router"
	AppDescription = "Routes URLs to Chrome profiles by regex"
	progID         = "URLRouterHTML"
	regAppKey      = "URLRouter" // Key name under RegisteredApplications and Run.

	clientKey         = `SOFTWARE\Clients\StartMenuInternet\URLRouter`
	capabilitiesKey   = clientKey + `\Capabilities`
	registeredAppsKey = `SOFTWARE\RegisteredApplications`
	classesProgIDKey  = `SOFTWARE\Classes\` + progID
	runKey            = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
	userChoiceKey     = `SOFTWARE\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`
)

// command builds the shell open command: "<exe>" "%1". Windows substitutes the
// clicked URL into %1; quoting it preserves URLs containing &, spaces, etc.
func command(exe string) string {
	return `"` + exe + `" "%1"`
}

// Register writes all HKCU keys that make urlrouter eligible as a default
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
		{classesProgIDKey, "", "URL Router Document"},
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

// IsDefault reports whether urlrouter is the current https handler, by reading
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
