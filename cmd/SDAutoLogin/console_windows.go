//go:build windows

package main

import (
	"os"
	"syscall"

	"sdautologin/internal/logging"
)

var (
	procAttachConsole = syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")
	procGetStdHandle  = syscall.NewLazyDLL("kernel32.dll").NewProc("GetStdHandle")
)

const (
	// Standard handles and the pseudo process id AttachConsole understands.
	stdOutputHandle     = 0xfffffff5
	stdErrorHandle      = 0xfffffff4
	attachParentProcess = 0xffffffff
)

// attachConsole gives the output of a command somewhere to appear. The executable
// is linked as a GUI program so a double click does not flash a console window,
// which also means it has no console of its own; started from a terminal it
// attaches to that terminal here. A process handed handles for a pipe or a
// redirected file is left alone, so its output keeps going where the caller put it.
//
// The caller opens the log first, so the warning below still reaches the log file
// when the console itself is missing.
func attachConsole() {
	if validStdHandle(stdOutputHandle) || validStdHandle(stdErrorHandle) {
		return
	}
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r == 0 {
		return
	}
	f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		logging.Log.Warn("could not open the console for output: %v", err)
		return
	}
	os.Stdout = f
	os.Stderr = f
}

func validStdHandle(which uintptr) bool {
	h, _, _ := procGetStdHandle.Call(which)
	return h != 0 && h != ^uintptr(0)
}
