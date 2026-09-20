//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	modUser32   = syscall.NewLazyDLL("user32.dll")
	modGdi32    = syscall.NewLazyDLL("gdi32.dll")
	modShell32  = syscall.NewLazyDLL("shell32.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW         = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW          = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW           = modUser32.NewProc("DefWindowProcW")
	procGetMessageW              = modUser32.NewProc("GetMessageW")
	procTranslateMessage         = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW         = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage          = modUser32.NewProc("PostQuitMessage")
	procPostMessageW             = modUser32.NewProc("PostMessageW")
	procDestroyWindow            = modUser32.NewProc("DestroyWindow")
	procSetForegroundWindow      = modUser32.NewProc("SetForegroundWindow")
	procGetCursorPos             = modUser32.NewProc("GetCursorPos")
	procTrackPopupMenu           = modUser32.NewProc("TrackPopupMenu")
	procCreatePopupMenu          = modUser32.NewProc("CreatePopupMenu")
	procAppendMenuW              = modUser32.NewProc("AppendMenuW")
	procDestroyMenu              = modUser32.NewProc("DestroyMenu")
	procLoadCursorW              = modUser32.NewProc("LoadCursorW")
	procMessageBoxW              = modUser32.NewProc("MessageBoxW")
	procGetSystemMetrics         = modUser32.NewProc("GetSystemMetrics")
	procGetUserDefaultUILanguage = modKernel32.NewProc("GetUserDefaultUILanguage")

	procShellNotifyIconW = modShell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW    = modShell32.NewProc("ShellExecuteW")

	procGetModuleHandleW = modKernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy = 0x0002
	wmApp     = 0x8000

	wmRButtonUp = 0x0205
	wmLButtonUp = 0x0202

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	// NIIF_INFO selects the information icon in a balloon tip.
	niiInfo = 0x00000001

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfChecked   = 0x00000008
	mfGrayed    = 0x00000001

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	swShowNormal = 1

	idcArrow = 32512

	// GetSystemMetrics index of the small icon size, see icon_windows.go.
	smCxSmIcon = 49
)

type point struct {
	X int32
	Y int32
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// msg matches the Win32 MSG layout (48 bytes on amd64).
type msg struct {
	Hwnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

// wndClassExW matches WNDCLASSEXW.
type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     syscall.Handle
	HIcon         syscall.Handle
	HCursor       syscall.Handle
	HbrBackground syscall.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       syscall.Handle
}

// notifyIconData matches NOTIFYICONDATAW (976 bytes on amd64). The padding
// needed by the C struct is inserted automatically because syscall.Handle is
// pointer sized.
type notifyIconData struct {
	CbSize           uint32
	HWnd             syscall.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            syscall.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UTimeoutVersion  uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         guid
	HBalloonIcon     syscall.Handle
}

func moduleHandle() uintptr {
	h, _, _ := procGetModuleHandleW.Call(0)
	return h
}

func u16ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

// putUTF16 copies s into a fixed size UTF-16 buffer, always NUL terminated.
func putUTF16(dst []uint16, s string) {
	for i := range dst {
		dst[i] = 0
	}
	u := utf16.Encode([]rune(s))
	if len(u) > len(dst)-1 {
		u = u[:len(dst)-1]
	}
	copy(dst, u)
}

func messageBox(owner syscall.Handle, title, text string, flags uint32) {
	procMessageBoxW.Call(uintptr(owner),
		uintptr(unsafe.Pointer(u16ptr(text))),
		uintptr(unsafe.Pointer(u16ptr(title))),
		uintptr(flags))
}

func loadSystemCursor() syscall.Handle {
	h, _, _ := procLoadCursorW.Call(0, idcArrow)
	return syscall.Handle(h)
}

const (
	winRunKey   = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	winRunValue = "SDAutoLogin"
)

func autostartEnabledWindows() bool {
	cmd := exec.Command("reg", "query", winRunKey, "/v", winRunValue)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run() == nil
}

func setAutostartWindows(on bool) bool {
	var cmd *exec.Cmd
	if on {
		cmd = exec.Command("reg", "add", winRunKey, "/v", winRunValue,
			"/t", "REG_SZ", "/d", launchCommand(), "/f")
	} else {
		cmd = exec.Command("reg", "delete", winRunKey, "/v", winRunValue, "/f")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		Log.Warn("updating autostart entry: %v", err)
		return false
	}
	return true
}
