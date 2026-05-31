//go:build windows

// Package winutil holds small Windows shell helpers shared by the tray UI.
package winutil

import "golang.org/x/sys/windows"

// ShellOpen asks the shell to open target with its default handler. It works
// for filesystem folders (opens Explorer) and for protocol URIs such as
// ms-settings:defaultapps (opens the Settings deep link, §3.3). It does not
// wait for the opened program.
func ShellOpen(target string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	const swShowNormal = 1
	return windows.ShellExecute(0, verb, file, nil, nil, swShowNormal)
}
