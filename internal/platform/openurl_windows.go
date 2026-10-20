//go:build windows

package platform

import (
	"fmt"
	"unsafe"
)

// OpenExternal hands a URL or a file to the shell. ShellExecuteW reports
// success with a value above 32; that is the only error signal it offers.
func OpenExternal(target string) error {
	verb := UTF16Ptr("open")
	file := UTF16Ptr(target)
	ret, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0, 0, swShowNormal)
	if int(ret) <= 32 {
		return fmt.Errorf("ShellExecuteW returned %d", int(ret))
	}
	return nil
}
