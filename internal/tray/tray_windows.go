//go:build windows

// Package tray implements TRAY mode (§6): the long-lived icon and menu that
// owns the editor window and the default-browser status indicator. The tray is
// the only persistent process; routing does not depend on it running.
package tray

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getlantern/systray"

	"guise/internal/assets"
	"guise/internal/config"
	"guise/internal/editor"
	"guise/internal/notify"
	"guise/internal/updater"
	"guise/internal/version"
	"guise/internal/winreg"
	"guise/internal/winutil"
)

// updateCheckInterval is how often the tray polls GitHub for a newer release
// while running, on top of the one check it runs at startup (§14). Releases are
// infrequent, so a daily check is plenty and keeps API traffic negligible.
const updateCheckInterval = 24 * time.Hour

// guiBusy serializes walk windows onto a single dedicated GUI thread: walk
// expects all its windows on one OS thread, and only one window is open at a
// time. guiQueue hands work to that thread.
var (
	guiBusy  atomic.Bool
	guiQueue = make(chan func(), 1)
)

// Run starts the tray and blocks until Quit. exe is the absolute path to this
// binary, used for the autostart Run value (§7).
func Run(exe string) {
	go guiThread()
	systray.Run(func() { onReady(exe) }, func() {})
}

// guiThread owns all walk windows. It runs each request to completion (the
// window's own message loop blocks here until closed) before taking the next.
func guiThread() {
	runtime.LockOSThread()
	for fn := range guiQueue {
		fn()
	}
}

// postGUI runs fn on the GUI thread if no window is currently open; otherwise
// it drops the request (the existing window already has focus).
func postGUI(fn func()) {
	if !guiBusy.CompareAndSwap(false, true) {
		return
	}
	guiQueue <- func() {
		defer guiBusy.Store(false)
		fn()
	}
}

func onReady(exe string) {
	// Remove the <exe>.old left by a previous self-update (§14). On the first
	// startup right after an update this may fail (the old image is still
	// exiting); the next startup clears it.
	updater.CleanupOld(exe)

	systray.SetIcon(assets.Icon)
	systray.SetTitle("Guise")
	systray.SetTooltip("Guise — route URLs to Chrome profiles")

	// Disabled header showing the running build, e.g. "Guise v1.2.3" (§6.1).
	mTitle := systray.AddMenuItem("Guise "+version.Short(), version.String())
	mTitle.Disable()
	systray.AddSeparator()
	mDefault := systray.AddMenuItem("Default browser: …", "Click to open Default Apps settings")
	mEdit := systray.AddMenuItem("Edit rules…", "Open the rule editor")
	mFolder := systray.AddMenuItem("Open config folder", "Open %APPDATA%\\Guise in Explorer")
	systray.AddSeparator()
	mTest := systray.AddMenuItem("Test a URL…", "See which rule would match a URL")
	// Update controls (§14). mInstall stays hidden until a verified download is
	// ready to apply.
	mUpdateNow := systray.AddMenuItem("Check for updates now…", "Check GitHub for a newer release")
	mInstall := systray.AddMenuItem("Install update", "Install the downloaded update and restart")
	mInstall.Hide()
	mAutoUpdate := systray.AddMenuItemCheckbox("Check for updates automatically", "Check GitHub for new releases in the background", false)
	systray.AddSeparator()
	mAutostart := systray.AddMenuItemCheckbox("Start at login", "Launch the tray when you sign in", false)
	mQuit := systray.AddMenuItem("Quit", "Exit Guise")

	if on, err := winreg.IsAutostart(); err == nil && on {
		mAutostart.Check()
	}

	// autoEnabled reads the current toggle from disk each time so the background
	// poll honors a change made via the checkbox without a restart. A malformed
	// config counts as disabled (the editor surfaces the error separately).
	autoEnabled := func() bool {
		cfg, err := config.Load()
		if err != nil {
			log.Printf("update toggle: %v", err)
			return false
		}
		return cfg.AutoUpdateEnabled()
	}
	if autoEnabled() {
		mAutoUpdate.Check()
	}

	// Shared state for a downloaded-but-not-yet-applied update, plus a guard so
	// a manual check can't run concurrently with the background one.
	var (
		updateMu    sync.Mutex
		pendingPath string
		pendingVer  string
		checking    atomic.Bool
	)
	exeDir := filepath.Dir(exe)

	// checkForUpdate runs the full check → download → verify flow (§14). It is
	// always safe to call: every failure is logged and, when manual, surfaced to
	// the user, but the tray keeps running. manual=false is the background path,
	// which stays silent unless an update is actually ready.
	checkForUpdate := func(manual bool) {
		if !checking.CompareAndSwap(false, true) {
			if manual {
				notify.Info("Guise", "An update check is already in progress.")
			}
			return
		}
		defer checking.Store(false)

		cur := version.Short()
		if !updater.IsReleaseBuild(cur) {
			log.Printf("update check skipped: development build %q", cur)
			if manual {
				notify.Info("Guise", "This is a development build ("+cur+").\n\nAuto-update only applies to released versions.")
			}
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		client := updater.NewClient()
		rel, err := client.Latest(ctx)
		if err != nil {
			log.Printf("update check: %v", err)
			if manual {
				notify.Error("Guise", "Could not check for updates:\n"+err.Error())
			}
			return
		}
		if !updater.IsNewer(cur, rel.TagName) {
			log.Printf("update check: up to date (current %s, latest %s)", cur, rel.TagName)
			if manual {
				notify.Info("Guise", "Guise is up to date ("+cur+").")
			}
			return
		}

		log.Printf("update available: %s -> %s; downloading", cur, rel.TagName)
		path, err := client.Download(ctx, rel, exeDir)
		if err != nil {
			log.Printf("update download: %v", err)
			if manual {
				notify.Error("Guise", "Found "+rel.TagName+", but the download failed:\n"+err.Error())
			}
			return
		}

		updateMu.Lock()
		pendingPath, pendingVer = path, rel.TagName
		updateMu.Unlock()

		mInstall.SetTitle("Install update " + rel.TagName)
		mInstall.Show()
		log.Printf("update %s downloaded and verified at %s", rel.TagName, path)
		notify.Info("Guise", "Guise "+rel.TagName+" has been downloaded and verified.\n\nChoose \"Install update "+rel.TagName+"\" in the tray menu to apply it and restart.")
	}

	// Live default-browser indicator (§6.1): poll so it reflects changes the
	// user makes in Settings without restarting the tray.
	refreshDefault := func() {
		switch isDef, err := winreg.IsDefault(); {
		case err != nil:
			mDefault.SetTitle("Default browser: unknown")
		case isDef:
			mDefault.SetTitle("✓ Default browser: Yes")
		default:
			mDefault.SetTitle("Default browser: No — click to fix")
		}
	}
	refreshDefault()
	done := make(chan struct{}) // closed on Quit to stop the poll goroutines.
	go func() {
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				refreshDefault()
			case <-done:
				return
			}
		}
	}()

	// Background update poll (§14): one check at startup, then daily, each gated
	// on the auto-update toggle so disabling it stops further background checks.
	go func() {
		if autoEnabled() {
			checkForUpdate(false)
		}
		t := time.NewTicker(updateCheckInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if autoEnabled() {
					checkForUpdate(false)
				}
			case <-done:
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case <-mDefault.ClickedCh:
				if isDef, _ := winreg.IsDefault(); !isDef {
					if err := winutil.ShellOpen("ms-settings:defaultapps"); err != nil {
						log.Printf("open default apps settings: %v", err)
					}
				}
			case <-mEdit.ClickedCh:
				postGUI(func() {
					if err := editor.Show(); err != nil {
						log.Printf("editor: %v", err)
					}
				})
			case <-mFolder.ClickedCh:
				_ = os.MkdirAll(config.Dir(), 0o755)
				if err := winutil.ShellOpen(config.Dir()); err != nil {
					log.Printf("open config folder: %v", err)
				}
			case <-mTest.ClickedCh:
				postGUI(func() {
					if err := editor.ShowTest(); err != nil {
						log.Printf("test dialog: %v", err)
					}
				})
			case <-mUpdateNow.ClickedCh:
				go checkForUpdate(true)
			case <-mInstall.ClickedCh:
				updateMu.Lock()
				path, ver := pendingPath, pendingVer
				updateMu.Unlock()
				if path == "" {
					continue // Nothing downloaded yet.
				}
				if !notify.Confirm("Guise", "Install update "+ver+" now?\n\nGuise will close and reopen.") {
					continue
				}
				if err := updater.Apply(exe, path); err != nil {
					log.Printf("apply update %s: %v", ver, err)
					notify.Error("Guise", "Could not install the update:\n"+err.Error())
					continue
				}
				// The replacement is already starting; shut this instance down
				// so only the new tray remains.
				log.Printf("applied update %s; relaunching and quitting", ver)
				close(done)
				systray.Quit()
				return
			case <-mAutoUpdate.ClickedCh:
				enable := !mAutoUpdate.Checked()
				cfg, err := config.Load()
				if err != nil {
					log.Printf("load config for auto-update toggle: %v", err)
					notify.Error("Guise", "Could not read the config to change this setting:\n"+err.Error())
					continue
				}
				cfg.SetAutoUpdate(enable)
				if err := config.Save(cfg); err != nil {
					log.Printf("save auto-update=%v: %v", enable, err)
					notify.Error("Guise", "Could not save this setting:\n"+err.Error())
					continue
				}
				if enable {
					mAutoUpdate.Check()
					go checkForUpdate(false) // Check right away on enable.
				} else {
					mAutoUpdate.Uncheck()
				}
			case <-mAutostart.ClickedCh:
				enable := !mAutostart.Checked()
				if err := winreg.SetAutostart(enable, exe); err != nil {
					log.Printf("set autostart=%v: %v", enable, err)
					continue
				}
				if enable {
					mAutostart.Check()
				} else {
					mAutostart.Uncheck()
				}
			case <-mQuit.ClickedCh:
				close(done)
				systray.Quit()
				return
			}
		}
	}()
}
