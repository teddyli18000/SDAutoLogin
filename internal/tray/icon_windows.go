//go:build windows

package tray

import (
	"image"
	"syscall"
	"unsafe"

	"sdautologin/internal/logging"
)

var (
	procCreateDIBSection   = modGdi32.NewProc("CreateDIBSection")
	procCreateBitmap       = modGdi32.NewProc("CreateBitmap")
	procDeleteObject       = modGdi32.NewProc("DeleteObject")
	procCreateIconIndirect = modUser32.NewProc("CreateIconIndirect")
)

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  syscall.Handle
	HbmColor syscall.Handle
}

// makeIcon turns a drawn RGBA image into an HICON. It builds a 32bpp top-down
// DIB section plus an empty AND mask, so per-pixel alpha needs no resource file.
// The header and the pixels themselves are laid out in dib.go, which has no
// build tag and can be tested where there is no GDI to call.
func makeIcon(img *image.RGBA) syscall.Handle {
	size := img.Bounds().Dx()
	if size <= 0 {
		return 0
	}

	hdr := dibHeader(size)
	var bits unsafe.Pointer
	hbmColor, _, _ := procCreateDIBSection.Call(
		0, uintptr(unsafe.Pointer(&hdr)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbmColor == 0 || bits == nil {
		logging.Log.Warn("CreateDIBSection failed for a %dx%d icon", size, size)
		return 0
	}

	writeDIBPixels(unsafe.Slice((*byte)(bits), size*size*4), img)

	// The AND mask is unused for 32bpp icons but CreateIconIndirect requires it.
	mask := andMask(size)
	hbmMask, _, _ := procCreateBitmap.Call(
		uintptr(size), uintptr(size), 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	if hbmMask == 0 {
		procDeleteObject.Call(hbmColor)
		logging.Log.Warn("CreateBitmap failed for a %dx%d icon", size, size)
		return 0
	}

	ii := iconInfo{FIcon: 1, HbmMask: syscall.Handle(hbmMask), HbmColor: syscall.Handle(hbmColor)}
	hicon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))

	procDeleteObject.Call(hbmColor)
	procDeleteObject.Call(hbmMask)
	if hicon == 0 {
		logging.Log.Warn("CreateIconIndirect failed for a %dx%d icon", size, size)
	}
	return syscall.Handle(hicon)
}

// trayIconSize follows the small icon metric so the tray is not blurry on
// scaled displays, falling back to the classic 16 px.
func trayIconSize() int {
	size, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	if size == 0 {
		return 16
	}
	return int(size)
}
