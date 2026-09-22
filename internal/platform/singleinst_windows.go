//go:build windows

package platform

import (
	"syscall"
	"unsafe"

	"sdautologin/internal/logging"
)

const (
	// Both names are per session, so two users can each run their own copy
	// while one user cannot start the program twice.
	instanceMutexName     = `Local\SDAutoLogin`
	showSettingsEventName = `Local\SDAutoLoginShowSettings`

	errorAlreadyExists = 183

	waitObject0  = 0
	waitInfinite = 0xffffffff
)

// instanceMutex and showSettingsEvent are held for the lifetime of the process.
// The system closes both when it ends, which releases the name and destroys the
// event once it can no longer be used.
var (
	instanceMutex     syscall.Handle
	showSettingsEvent syscall.Handle

	// instanceTaken remembers that this process already owns the name. Asking a
	// second time would report the name as taken, because the mutex we hold
	// counts as an existing object.
	instanceTaken bool
)

// AcquireSingleInstance reports whether this is the only running copy. When
// another copy owns the name and requestSettings is set, that copy is asked to
// open its settings page, so starting the program twice brings up the existing
// window instead of adding a second tray icon.
func AcquireSingleInstance(requestSettings bool) bool {
	if instanceTaken {
		return true
	}

	// The event is created before the mutex on purpose. A second copy can only
	// discover us by finding the mutex, and it can only find the mutex once we
	// created it, so at that point the event is guaranteed to exist and to be
	// held by us. The other order would let a quick second copy signal an event
	// that we have not created yet, and the request would be lost with it.
	if ev, _, err := procCreateEventW.Call(0, 1, 0, uintptr(unsafe.Pointer(UTF16Ptr(showSettingsEventName)))); ev != 0 {
		showSettingsEvent = syscall.Handle(ev)
	} else {
		logging.Log.Warn("could not create the settings request event: %v", err)
	}

	h, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(UTF16Ptr(instanceMutexName))))
	if h == 0 {
		logging.Log.Warn("could not create the instance mutex: %v", lastErr)
		return true
	}
	if errno, ok := lastErr.(syscall.Errno); ok && uint32(errno) == errorAlreadyExists {
		logging.Log.Info("another instance is already running, not starting a second one")
		if requestSettings {
			requestSettingsPage()
		}
		return false
	}
	instanceMutex = syscall.Handle(h)
	instanceTaken = true
	return true
}

// requestSettingsPage hands the request to the copy that is already running.
// The event is manual reset, so it stays signalled until that copy looks at it.
// A request therefore survives until the running copy is ready, which a posted
// message could not promise while its window is still being created.
func requestSettingsPage() {
	if showSettingsEvent == 0 {
		logging.Log.Warn("the running instance cannot be asked for its settings page")
		return
	}
	logging.Log.Info("asking the running instance to open its settings page")
	procSetEvent.Call(uintptr(showSettingsEvent))
	// The handle is closed by the system when this process ends, a moment away.
}

// WaitForSettingsRequest calls f for every request a later copy makes. It
// blocks on its own goroutine and returns only if waiting fails.
func WaitForSettingsRequest(f func()) {
	if showSettingsEvent == 0 {
		return
	}
	for {
		r, _, _ := procWaitForSingleObject.Call(uintptr(showSettingsEvent), waitInfinite)
		if uint32(r) != waitObject0 {
			logging.Log.Warn("waiting for a settings request failed: %#x", uint32(r))
			return
		}
		// Clear the request before handling it, otherwise the loop would keep
		// seeing the signalled state.
		procResetEvent.Call(uintptr(showSettingsEvent))
		f()
	}
}
