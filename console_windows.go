//go:build windows

package main

import (
	"os"
	"syscall"
)

// AttachConsole(ATTACH_PARENT_PROCESS): connect to the console of the process
// that launched us. (DWORD)-1 is ATTACH_PARENT_PROCESS.
var (
	modkernel32       = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole = modkernel32.NewProc("AttachConsole")
)

const attachParentProcess = ^uintptr(0)

// attachParentConsole connects this GUI-subsystem binary to the console of the
// terminal that launched it and returns a writer to that console, or nil if
// there is no parent console (e.g. the exe was double-clicked). A -H windowsgui
// binary has no console of its own (§8), so without this, output written for
// flags like --version would go nowhere on an interactive launch. The caller
// owns the returned file and must Close it.
func attachParentConsole() *os.File {
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r == 0 {
		return nil
	}
	// Write to the console's active output buffer directly: our own os.Stdout
	// was bound at process start (before the attach) and is not connected here.
	con, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return nil
	}
	return con
}
