// Package platform holds the operating system glue the rest of the program
// needs: handing a URL or file to the desktop, the autostart entry and the
// single instance check. Each concern has one file per platform family.
package platform

import (
	"syscall"
)

var (
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")
	modShell32  = syscall.NewLazyDLL("shell32.dll")

	procShellExecuteW = modShell32.NewProc("ShellExecuteW")

	procCreateMutexW        = modKernel32.NewProc("CreateMutexW")
	procCreateEventW        = modKernel32.NewProc("CreateEventW")
	procSetEvent            = modKernel32.NewProc("SetEvent")
	procResetEvent          = modKernel32.NewProc("ResetEvent")
	procWaitForSingleObject = modKernel32.NewProc("WaitForSingleObject")
)

// swShowNormal is the SW_SHOWNORMAL show command ShellExecuteW takes.
const swShowNormal = 1

// UTF16Ptr converts a Go string to the NUL terminated UTF-16 buffer the Win32
// wide character entry points expect. An unrepresentable string becomes the
// empty one, so a call cannot crash on bad input.
func UTF16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}
