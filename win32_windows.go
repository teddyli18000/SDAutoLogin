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

	procRegisterClassExW     = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW      = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW       = modUser32.NewProc("DefWindowProcW")
	procGetMessageW          = modUser32.NewProc("GetMessageW")
	procTranslateMessage     = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW     = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage      = modUser32.NewProc("PostQuitMessage")
	procPostMessageW         = modUser32.NewProc("PostMessageW")
	procDestroyWindow        = modUser32.NewProc("DestroyWindow")
	procShowWindow           = modUser32.NewProc("ShowWindow")
	procSetForegroundWindow  = modUser32.NewProc("SetForegroundWindow")
	procSetFocus             = modUser32.NewProc("SetFocus")
	procGetCursorPos         = modUser32.NewProc("GetCursorPos")
	procTrackPopupMenu       = modUser32.NewProc("TrackPopupMenu")
	procCreatePopupMenu      = modUser32.NewProc("CreatePopupMenu")
	procAppendMenuW          = modUser32.NewProc("AppendMenuW")
	procDestroyMenu          = modUser32.NewProc("DestroyMenu")
	procLoadIconW            = modUser32.NewProc("LoadIconW")
	procLoadCursorW          = modUser32.NewProc("LoadCursorW")
	procSendMessageW         = modUser32.NewProc("SendMessageW")
	procGetWindowTextW       = modUser32.NewProc("GetWindowTextW")
	procSetWindowTextW       = modUser32.NewProc("SetWindowTextW")
	procGetWindowTextLengthW = modUser32.NewProc("GetWindowTextLengthW")
	procMessageBoxW          = modUser32.NewProc("MessageBoxW")
	procAdjustWindowRectEx   = modUser32.NewProc("AdjustWindowRectEx")
	procGetWindowRect        = modUser32.NewProc("GetWindowRect")
	procSetWindowPos         = modUser32.NewProc("SetWindowPos")
	procGetSystemMetrics     = modUser32.NewProc("GetSystemMetrics")

	procGetStockObject = modGdi32.NewProc("GetStockObject")

	procShellNotifyIconW = modShell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW    = modShell32.NewProc("ShellExecuteW")

	procGetModuleHandleW = modKernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy = 0x0002
	wmClose   = 0x0010
	wmCommand = 0x0111
	wmSetFont = 0x0030
	wmApp     = 0x8000

	wmRButtonUp     = 0x0205
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203

	bnClicked = 0

	bmGetCheck = 0x00F0
	bmSetCheck = 0x00F1
	bstChecked = 1

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfChecked   = 0x00000008
	mfGrayed    = 0x00000001

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	swHide       = 0
	swShowNormal = 1

	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsVisible      = 0x10000000
	wsChild        = 0x40000000
	wsBorder       = 0x00800000
	wsTabStop      = 0x00010000
	esPassword     = 0x0020
	esAutoHScroll  = 0x0080
	bsAutoCheckBox = 0x00000003

	swpNoSize     = 0x0001
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	smCxScreen = 0
	smCyScreen = 1

	defaultGuiFont = 17
	idcArrow       = 32512

	idiApplication = 32512
	idiError       = 32513
	idiWarning     = 32515
	idiInformation = 32516
)

type point struct {
	X int32
	Y int32
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
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

func getWindowText(h syscall.Handle) string {
	n, _, _ := procGetWindowTextLengthW.Call(uintptr(h))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, int(n)+1)
	got, _, _ := procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if got == 0 {
		return ""
	}
	return string(utf16.Decode(buf[:got]))
}

func setWindowText(h syscall.Handle, s string) {
	procSetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(u16ptr(s))))
}

func sendMessage(h syscall.Handle, msg uint32, w, l uintptr) uintptr {
	r, _, _ := procSendMessageW.Call(uintptr(h), uintptr(msg), w, l)
	return r
}

func messageBox(owner syscall.Handle, title, text string, flags uint32) {
	procMessageBoxW.Call(uintptr(owner),
		uintptr(unsafe.Pointer(u16ptr(text))),
		uintptr(unsafe.Pointer(u16ptr(title))),
		uintptr(flags))
}

// createControl adds a child control and applies the default GUI font, which
// newly created controls otherwise do not inherit.
func createControl(parent syscall.Handle, class, text string, style uint32, x, y, w, h int32, id uintptr) syscall.Handle {
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(u16ptr(class))),
		uintptr(unsafe.Pointer(u16ptr(text))),
		uintptr(style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(parent), id, moduleHandle(), 0)
	if hwnd == 0 {
		return 0
	}
	return syscall.Handle(hwnd)
}

func guiFont() uintptr {
	f, _, _ := procGetStockObject.Call(defaultGuiFont)
	return f
}

func centerWindow(hwnd uintptr) {
	var r rect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	sw, _, _ := procGetSystemMetrics.Call(smCxScreen)
	sh, _, _ := procGetSystemMetrics.Call(smCyScreen)
	x := (int32(sw) - (r.Right - r.Left)) / 2
	y := (int32(sh) - (r.Bottom - r.Top)) / 2
	procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

func openWithShell(path string) {
	verb := u16ptr("open")
	file := u16ptr(path)
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, swShowNormal)
}

func loadSystemIcon(id uintptr) syscall.Handle {
	h, _, _ := procLoadIconW.Call(0, id)
	return syscall.Handle(h)
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
