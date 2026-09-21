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

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  syscall.Handle
	HbmColor syscall.Handle
}

const (
	dibRGBColors = 0
	biRGB        = 0
)

// makeIcon turns a drawn RGBA image into an HICON via a 32bpp top-down DIB
// section plus an empty AND mask, which is the standard way to create an icon
// with per-pixel alpha without shipping a resource file.
func makeIcon(img *image.RGBA) syscall.Handle {
	size := img.Bounds().Dx()
	if size <= 0 {
		return 0
	}

	hdr := bitmapInfoHeader{
		Size:        40,
		Width:       int32(size),
		Height:      -int32(size), // negative height: rows are stored top-down
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}
	var bits unsafe.Pointer
	hbmColor, _, _ := procCreateDIBSection.Call(
		0, uintptr(unsafe.Pointer(&hdr)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbmColor == 0 || bits == nil {
		logging.Log.Warn("CreateDIBSection failed for a %dx%d icon", size, size)
		return 0
	}

	// Windows wants BGRA in memory.
	pixels := unsafe.Slice((*byte)(bits), size*size*4)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := img.RGBAAt(x, y)
			o := (y*size + x) * 4
			pixels[o+0] = c.B
			pixels[o+1] = c.G
			pixels[o+2] = c.R
			pixels[o+3] = c.A
		}
	}

	// The AND mask is unused for 32bpp icons but CreateIconIndirect requires it.
	maskStride := ((size + 31) / 32) * 4
	mask := make([]byte, maskStride*size)
	hbmMask, _, _ := procCreateBitmap.Call(
		uintptr(size), uintptr(size), 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	if hbmMask == 0 {
		procDeleteObject.Call(hbmColor)
		logging.Log.Warn("CreateBitmap failed for a %dx%d icon", size, size)
		return 0
	}

	ii := iconInfo{FIcon: 1, HbmMask: syscall.Handle(hbmMask), HbmColor: syscall.Handle(hbmColor)}
	hicon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))

	// The icon owns copies of both bitmaps now.
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
