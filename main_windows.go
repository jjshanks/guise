//go:build windows

// Command guise is a single Windows binary with three modes (§2), selected
// by how it is invoked:
//
//	guise.exe <url>         ROUTE mode  — match, launch Chrome, exit.
//	guise.exe --tray        TRAY mode   — tray icon + rule editor.
//	guise.exe --register    SETUP mode  — write HKCU registry entries.
//	guise.exe --unregister  SETUP mode  — remove them.
//
// Every invocation is self-contained and self-routing: Windows runs this same
// exe for each clicked link, so ROUTE mode re-reads config from disk each time.
package main

// Regenerate the embedded manifest + icon resource (run `go generate`):
//go:generate rsrc -manifest guise.manifest -ico icon.ico -arch amd64 -o rsrc_windows_amd64.syso

import (
	"log"
	"os"
	"strings"

	"guise/internal/applog"
	"guise/internal/notify"
	"guise/internal/router"
	"guise/internal/tray"
	"guise/internal/winreg"
)

// main keeps no logic of its own beyond translating run's exit code: calling
// os.Exit here (rather than inside run) is what lets run's deferred log close
// actually execute before the process ends.
func main() {
	os.Exit(run())
}

// run dispatches on argv and returns the process exit code.
func run() int {
	if f, err := applog.Setup(); err != nil {
		log.Printf("logging setup failed, using default: %v", err)
	} else {
		defer f.Close()
	}

	args := os.Args[1:]
	switch {
	case len(args) == 0:
		// No argument in ROUTE mode: launch Chrome normally (§10).
		return routeExit(router.Route(""))
	case args[0] == "--unregister":
		// Unregister only deletes keys, so it needs no exe path.
		return unregister()
	case args[0] == "--register":
		exe, ok := selfPath()
		if !ok {
			return 1
		}
		return register(exe)
	case args[0] == "--tray":
		exe, ok := selfPath()
		if !ok {
			return 1
		}
		tray.Run(exe) // Blocks until Quit.
		return 0
	case strings.HasPrefix(args[0], "-"):
		// A URL Windows hands us always starts with a scheme, never a dash.
		// An unrecognized flag is a misinvocation; forwarding it to Chrome as a
		// "URL" would let Chrome parse it as a switch, so reject it instead.
		log.Printf("unknown flag %q (expected a URL or a mode flag)", args[0])
		return 2
	default:
		// ROUTE mode: the first argument is the URL Windows handed us (§3.1).
		return routeExit(router.Route(args[0]))
	}
}

// selfPath resolves this executable's path, which --register and --tray bake
// into the registry. On failure it surfaces the problem and reports !ok: an
// empty or wrong path would silently produce a default-browser registration
// that launches nothing, so these modes must fail loudly rather than continue.
func selfPath() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("resolving own path: %v", err)
		notify.Error("Guise", "Could not determine the Guise executable path.\n\nSetup cannot continue.")
		return "", false
	}
	return exe, true
}

func register(exe string) int {
	if err := winreg.Register(exe); err != nil {
		log.Printf("register: %v", err)
		notify.Error("Guise", "Registration failed:\n"+err.Error())
		return 1
	}
	log.Printf("registered exe=%q", exe)

	msg := "Guise is now registered as an available browser.\n\n"
	if isDef, _ := winreg.IsDefault(); isDef {
		msg += "It is already your default browser."
	} else {
		// Windows 11 forbids silent default changes (§3.3); guide the user.
		msg += "To finish, set it as your default in:\nSettings → Apps → Default apps → Guise → Set default."
	}
	notify.Info("Guise", msg)
	return 0
}

func unregister() int {
	if err := winreg.Unregister(); err != nil {
		log.Printf("unregister: %v", err)
		notify.Error("Guise", "Unregister failed:\n"+err.Error())
		return 1
	}
	log.Printf("unregistered")
	notify.Info("Guise", "Guise has been unregistered.")
	return 0
}

// routeExit maps a routing error to a non-zero exit code for scripted use,
// without printing (the windowsgui binary has no console).
func routeExit(err error) int {
	if err != nil {
		return 1
	}
	return 0
}
