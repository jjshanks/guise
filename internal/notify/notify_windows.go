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
	mbYesNo         = 0x00000004
	mbIconError     = 0x00000010
	mbIconQuestion  = 0x00000020
	mbIconInfo      = 0x00000040
	mbSetForeground = 0x00010000

	idYes = 6 // MessageBoxW return value for the Yes button.
)

// Error pops a modal error dialog with the given title and message. It blocks
// until the user dismisses it, which is acceptable here: there is nothing else
// the failed ROUTE process can usefully do.
func Error(title, message string) { box(title, message, mbOK|mbIconError) }

// Info pops a modal information dialog. Used by --register/--unregister to
// confirm the result, since the windowsgui binary has no console (§8).
func Info(title, message string) { box(title, message, mbOK|mbIconInfo) }

// Confirm pops a modal Yes/No dialog and reports whether the user chose Yes.
// The tray uses it to confirm installing a downloaded update before it replaces
// the running binary (§14). A string it cannot render (a NUL byte) counts as
// No: proceeding without a real confirmation would be wrong.
func Confirm(title, message string) bool {
	return box(title, message, mbYesNo|mbIconQuestion) == idYes
}

func box(title, message string, flags uintptr) uintptr {
	// UTF16PtrFromString fails only if the string contains a NUL byte. If it
	// does, skip the box rather than hand MessageBoxW a null pointer — there is
	// nothing else this path can usefully do, and a crash here would defeat the
	// purpose of surfacing the original error.
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	m, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return 0
	}
	r, _, _ := procMessageBox.Call(
		0,
		uintptr(unsafe.Pointer(m)),
		uintptr(unsafe.Pointer(t)),
		flags|mbSetForeground,
	)
	return r
}
