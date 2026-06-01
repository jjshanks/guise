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

	"guise/internal/applog"
	"guise/internal/notify"
	"guise/internal/router"
	"guise/internal/tray"
	"guise/internal/winreg"
)

func main() {
	if f, err := applog.Setup(); err != nil {
		log.Printf("logging setup failed, using default: %v", err)
	} else {
		defer f.Close()
	}

	exe, err := os.Executable()
	if err != nil {
		log.Printf("resolving own path: %v", err)
	}

	args := os.Args[1:]
	switch {
	case len(args) == 0:
		// No argument in ROUTE mode: launch Chrome normally (§10).
		exitOnErr(router.Route(""))
	case args[0] == "--tray":
		tray.Run(exe) // Blocks until Quit.
	case args[0] == "--register":
		register(exe)
	case args[0] == "--unregister":
		unregister()
	default:
		// ROUTE mode: the first argument is the URL Windows handed us (§3.1).
		exitOnErr(router.Route(args[0]))
	}
}

func register(exe string) {
	if err := winreg.Register(exe); err != nil {
		log.Printf("register: %v", err)
		notify.Error("Guise", "Registration failed:\n"+err.Error())
		os.Exit(1)
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
}

func unregister() {
	if err := winreg.Unregister(); err != nil {
		log.Printf("unregister: %v", err)
		notify.Error("Guise", "Unregister failed:\n"+err.Error())
		os.Exit(1)
	}
	log.Printf("unregistered")
	notify.Info("Guise", "Guise has been unregistered.")
}

// exitOnErr leaves a non-zero exit code on routing failure for scripted use,
// without printing (the windowsgui binary has no console).
func exitOnErr(err error) {
	if err != nil {
		os.Exit(1)
	}
}
