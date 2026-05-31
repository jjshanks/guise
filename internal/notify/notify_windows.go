//go:build windows

// Package notify shows the user a Windows message when routing cannot proceed
// (§10) — e.g. Chrome is not installed. It uses MessageBoxW directly so it
// works from the short-lived ROUTE process with no tray or window.
package notify

import (
	"syscall"
	"unsafe"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
)

const (
	mbOK            = 0x00000000
	mbIconError     = 0x00000010
	mbIconInfo      = 0x00000040
	mbSetForeground = 0x00010000
)

// Error pops a modal error dialog with the given title and message. It blocks
// until the user dismisses it, which is acceptable here: there is nothing else
// the failed ROUTE process can usefully do.
func Error(title, message string) { box(title, message, mbIconError) }

// Info pops a modal information dialog. Used by --register/--unregister to
// confirm the result, since the windowsgui binary has no console (§8).
func Info(title, message string) { box(title, message, mbIconInfo) }

func box(title, message string, icon uintptr) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(message)
	procMessageBox.Call(
		0,
		uintptr(unsafe.Pointer(m)),
		uintptr(unsafe.Pointer(t)),
		mbOK|icon|mbSetForeground,
	)
}
