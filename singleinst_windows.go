//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const (
	// instanceMutexName is per session, so two users can each run their own
	// copy while one user cannot start the program twice.
	instanceMutexName  = `Local\SDAutoLogin`
	showSettingsName   = "SDAutoLoginShowSettings"
	hwndBroadcast      = 0xffff
	errorAlreadyExists = 183
)

var (
	procCreateMutexW           = modKernel32.NewProc("CreateMutexW")
	procRegisterWindowMessageW = modUser32.NewProc("RegisterWindowMessageW")
)

// instanceMutex is held open for the lifetime of the process, which is what
// keeps the name taken. The system releases it when the process ends.
var instanceMutex syscall.Handle

// acquireSingleInstance reports whether this is the only running copy. When
// another copy owns the name and requestSettings is set, that copy is asked to
// open its settings page, so starting the program twice brings up the existing
// window instead of adding a second tray icon.
func acquireSingleInstance(requestSettings bool) bool {
	h, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(u16ptr(instanceMutexName))))
	if h == 0 {
		Log.Warn("could not create the instance mutex: %v", lastErr)
		return true
	}
	if errno, ok := lastErr.(syscall.Errno); ok && uint32(errno) == errorAlreadyExists {
		Log.Info("another instance is already running, not starting a second one")
		if requestSettings {
			askForSettingsPage()
		}
		return false
	}
	instanceMutex = syscall.Handle(h)
	return true
}

// showSettingsMessage returns the id of the message that asks the running copy
// to show its settings page. Both copies register the same name, so they agree
// on the number.
func showSettingsMessage() uint32 {
	id, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(u16ptr(showSettingsName))))
	return uint32(id)
}

func askForSettingsPage() {
	id := showSettingsMessage()
	if id == 0 {
		return
	}
	Log.Debug("asking the running instance to open its settings page")
	procPostMessageW.Call(hwndBroadcast, uintptr(id), 0, 0)
}
