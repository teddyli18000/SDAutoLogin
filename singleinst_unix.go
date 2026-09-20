//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// instanceLock is held for the lifetime of the process. The kernel drops the
// lock when the process ends, so a crash cannot leave a stale lock behind.
var instanceLock *os.File

// acquireSingleInstance takes an exclusive lock on a file in the temporary
// directory. The running copy cannot be asked to show its page from here, its
// tray icon is already there to click, so requestSettings is ignored.
func acquireSingleInstance(_ bool) bool {
	path := filepath.Join(os.TempDir(), "sdautologin.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		Log.Warn("could not open the lock file %s: %v", path, err)
		return true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		Log.Info("another instance is already running, not starting a second one")
		_ = f.Close()
		return false
	}
	instanceLock = f
	return true
}
