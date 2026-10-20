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

// attachConsole gives the output of a command somewhere to appear. Linked as a
// GUI program, the binary has no console of its own, so it attaches to the
// terminal that started it. A process handed a pipe or a file is left alone.
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
