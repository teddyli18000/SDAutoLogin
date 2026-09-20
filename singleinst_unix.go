//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// instanceLock is held for the lifetime of the process. The kernel drops the
// lock when the process ends, so a crash cannot leave a stale lock behind.
// Keeping a reference to the file also keeps it from being closed by the
// garbage collector, which would release the lock while the program runs.
var instanceLock *os.File

// instanceTaken remembers that this process already holds the lock. A second
// lock request on a fresh descriptor fails even inside one process, so asking
// twice would wrongly report another instance.
var instanceTaken bool

// acquireSingleInstance takes an exclusive lock on a file in the temporary
// directory. The running copy cannot be asked to show its page from here, its
// tray icon is already there to click, so requestSettings is ignored.
func acquireSingleInstance(_ bool) bool {
	if instanceTaken {
		return true
	}
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
	instanceTaken = true
	return true
}
