//go:build windows

package main

import (
	"fmt"
	"unsafe"
)

// openExternal hands a URL or a file to the shell. ShellExecuteW reports
// success with a value above 32, which is the only error signal it offers.
func openExternal(target string) error {
	verb := u16ptr("open")
	file := u16ptr(target)
	ret, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0, 0, swShowNormal)
	if int(ret) <= 32 {
		return fmt.Errorf("ShellExecuteW returned %d", int(ret))
	}
	return nil
}
