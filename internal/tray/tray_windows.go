//go:build windows

// Package tray implements TRAY mode (§6): the long-lived icon and menu that
// owns the editor window and the default-browser status indicator. The tray is
// the only persistent process; routing does not depend on it running.
package tray

import (
	"log"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/getlantern/systray"

	"guise/internal/assets"
	"guise/internal/config"
	"guise/internal/editor"
	"guise/internal/winreg"
	"guise/internal/winutil"
)

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
	systray.SetIcon(assets.Icon)
	systray.SetTitle("Guise")
	systray.SetTooltip("Guise — route URLs to Chrome profiles")

	mTitle := systray.AddMenuItem("Guise", "")
	mTitle.Disable()
	systray.AddSeparator()
	mDefault := systray.AddMenuItem("Default browser: …", "Click to open Default Apps settings")
	mEdit := systray.AddMenuItem("Edit rules…", "Open the rule editor")
	mFolder := systray.AddMenuItem("Open config folder", "Open %APPDATA%\\Guise in Explorer")
	systray.AddSeparator()
	mTest := systray.AddMenuItem("Test a URL…", "See which rule would match a URL")
	systray.AddSeparator()
	mAutostart := systray.AddMenuItemCheckbox("Start at login", "Launch the tray when you sign in", false)
	mQuit := systray.AddMenuItem("Quit", "Exit Guise")

	if on, err := winreg.IsAutostart(); err == nil && on {
		mAutostart.Check()
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
	done := make(chan struct{}) // closed on Quit to stop the poll goroutine.
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
