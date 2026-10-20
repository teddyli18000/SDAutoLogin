//go:build windows && !notray

package tray

import (
	"testing"
	"unsafe"
)

// TestTheWin32StructsHaveTheLayoutTheOSWrites covers the sizes of the structs
// this package passes to Win32 by pointer. The OS writes into them at the
// offsets of its own headers, so a field out of order or a handle that is not
// pointer sized does not fail to compile: GetMessageW fills the wrong bytes and
// the tray stops answering, or the message loop walks off the end of the struct.
//
// Both widths are published: the amd64 and arm64 builds have 8 byte handles and
// the 386 build has 4.
func TestTheWin32StructsHaveTheLayoutTheOSWrites(t *testing.T) {
	sizes := map[uintptr]struct{ msg, wnd, nid uintptr }{
		8: {msg: 48, wnd: 80, nid: 976}, // the documented amd64 sizes
		4: {msg: 28, wnd: 48, nid: 956}, // and the 386 ones
	}
	width := unsafe.Sizeof(uintptr(0))
	want, documented := sizes[width]
	if !documented {
		t.Fatalf("no documented Win32 layout for a %d byte handle", width)
	}

	if got := unsafe.Sizeof(msg{}); got != want.msg {
		t.Errorf("msg is %d bytes, want the %d of MSG", got, want.msg)
	}
	if got := unsafe.Sizeof(wndClassExW{}); got != want.wnd {
		t.Errorf("wndClassExW is %d bytes, want the %d of WNDCLASSEXW", got, want.wnd)
	}
	if got := unsafe.Sizeof(notifyIconData{}); got != want.nid {
		t.Errorf("notifyIconData is %d bytes, want the %d of NOTIFYICONDATAW", got, want.nid)
	}
	if got := unsafe.Sizeof(guid{}); got != 16 {
		t.Errorf("guid is %d bytes, want the 16 of a GUID", got)
	}

	// The text fields are fixed arrays in the middle of that struct, and the
	// tooltip writer fills them up to their length. A shorter array would move
	// every field after it.
	var nid notifyIconData
	if len(nid.SzTip) != 128 || len(nid.SzInfo) != 256 || len(nid.SzInfoTitle) != 64 {
		t.Errorf("the text fields are %d/%d/%d units, want the 128/256/64 of NOTIFYICONDATAW",
			len(nid.SzTip), len(nid.SzInfo), len(nid.SzInfoTitle))
	}
}

// TestTheWin32FlagsAreTheOnesTheOSDefines covers the numbers the tray hands to
// Win32. The compiler cannot check them and a wrong one fails quietly: NIF_TIP
// that is really NIF_ICON leaves the tooltip blank, and a menu flag off by a bit
// draws an entry as enabled that the program means to grey out.
func TestTheWin32FlagsAreTheOnesTheOSDefines(t *testing.T) {
	cases := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"NIM_ADD", nimAdd, 0x0},
		{"NIM_MODIFY", nimModify, 0x1},
		{"NIM_DELETE", nimDelete, 0x2},
		{"NIF_MESSAGE", nifMessage, 0x1},
		{"NIF_ICON", nifIcon, 0x2},
		{"NIF_TIP", nifTip, 0x4},
		{"NIF_INFO", nifInfo, 0x10},
		{"NIIF_INFO", niiInfo, 0x1},
		{"MF_STRING", mfString, 0x0},
		{"MF_SEPARATOR", mfSeparator, 0x800},
		{"MF_CHECKED", mfChecked, 0x8},
		{"MF_GRAYED", mfGrayed, 0x1},
		{"TPM_RIGHTBUTTON", tpmRightButton, 0x2},
		{"TPM_RETURNCMD", tpmReturnCmd, 0x100},
		{"WM_DESTROY", wmDestroy, 0x2},
		{"WM_APP", wmApp, 0x8000},
		{"WM_LBUTTONUP", wmLButtonUp, 0x202},
		{"WM_RBUTTONUP", wmRButtonUp, 0x205},
		{"IDC_ARROW", idcArrow, 32512},
		{"SM_CXSMICON", smCxSmIcon, 49},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s is %#x, want %#x", c.name, c.got, c.want)
		}
	}
}

// TestTheTrayMessagesAndCommandsAreDistinct covers the two sets of numbers the
// tray routes on. The messages it posts to its own window must stay in the range
// WM_APP reserves, or a window procedure would take them for a system message of
// the same number, and the menu commands must be distinct or a menu entry would
// run what another one means to.
func TestTheTrayMessagesAndCommandsAreDistinct(t *testing.T) {
	messages := map[string]uint32{
		"wmTrayIcon":      wmTrayIcon,
		"wmTrayRefresh":   wmTrayRefresh,
		"wmQuitRequested": wmQuitRequested,
		"wmShowSettings":  wmShowSettings,
	}
	seen := make(map[uint32]string, len(messages))
	for name, id := range messages {
		if id < wmApp {
			t.Errorf("%s is %#x, below the WM_APP range of %#x", name, id, wmApp)
		}
		if other, twice := seen[id]; twice {
			t.Errorf("%s and %s are both %#x", name, other, id)
		}
		seen[id] = name
	}

	commands := map[string]uint32{
		"cmdCheckNow":  cmdCheckNow,
		"cmdSettings":  cmdSettings,
		"cmdReload":    cmdReload,
		"cmdOpenCfg":   cmdOpenCfg,
		"cmdAutostart": cmdAutostart,
		"cmdOpenLog":   cmdOpenLog,
		"cmdExit":      cmdExit,
	}
	seen = make(map[uint32]string, len(commands))
	for name, id := range commands {
		if other, twice := seen[id]; twice {
			t.Errorf("%s and %s are both the menu command %d", name, other, id)
		}
		seen[id] = name
	}
}
