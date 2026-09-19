//go:build windows

package main

// setAutostart toggles the HKCU Run entry, see win32_windows.go.
func setAutostart(on bool) bool { return setAutostartWindows(on) }

func autostartEnabled() bool { return autostartEnabledWindows() }
