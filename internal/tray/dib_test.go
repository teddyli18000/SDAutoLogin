package tray

import (
	"bytes"
	"image"
	"image/color"
	"testing"
	"unsafe"
)

// TestTheHeaderIsTheSizeTheOSReadsFirst covers the one thing CreateDIBSection
// reads before it reads anything else. BITMAPINFOHEADER is a documented 40
// bytes, and a header of another size makes the call fail on every icon.
func TestTheHeaderIsTheSizeTheOSReadsFirst(t *testing.T) {
	if got := unsafe.Sizeof(bitmapInfoHeader{}); got != 40 {
		t.Errorf("bitmapInfoHeader is %d bytes, want the 40 of BITMAPINFOHEADER", got)
	}
}

// TestTheHeaderDescribesATopDown32bppBitmap covers the fields that decide how
// the pixels written below are read back. A positive height would put the first
// row of the buffer at the bottom and draw the icon upside down, and anything
// but one plane of 32 BI_RGB bits would drop the per pixel alpha the mark is
// made of.
func TestTheHeaderDescribesATopDown32bppBitmap(t *testing.T) {
	for _, size := range []int{16, 22, 32, 256} {
		hdr := dibHeader(size)
		if got := hdr.Size; got != uint32(unsafe.Sizeof(hdr)) {
			t.Errorf("dibHeader(%d) says it is %d bytes, but it is %d", size, got, unsafe.Sizeof(hdr))
		}
		if hdr.Width != int32(size) {
			t.Errorf("dibHeader(%d).Width = %d", size, hdr.Width)
		}
		if hdr.Height != -int32(size) {
			t.Errorf("dibHeader(%d).Height = %d, want %d for rows stored top-down", size, hdr.Height, -size)
		}
		if hdr.Planes != 1 || hdr.BitCount != 32 {
			t.Errorf("dibHeader(%d) has %d planes of %d bits, want 1 plane of 32", size, hdr.Planes, hdr.BitCount)
		}
		if hdr.Compression != biRGB {
			t.Errorf("dibHeader(%d).Compression = %d, want %d (BI_RGB)", size, hdr.Compression, biRGB)
		}
	}
}

// TestThePixelsAreStoredAsBGRAFromTheTopDown covers the byte work that turns the
// drawn mark into the buffer the window manager reads. A DIB is BGRA while the
// drawing is RGBA, and its rows run top-down with no padding at 32bpp; getting
// either wrong swaps the red and blue of the icon, or mirrors it, without any
// call failing.
func TestThePixelsAreStoredAsBGRAFromTheTopDown(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	// Every channel of every pixel differs, so a swapped pair or a transposed
	// row cannot pass by accident.
	img.SetRGBA(0, 0, color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x44})
	img.SetRGBA(1, 0, color.RGBA{R: 0x55, G: 0x66, B: 0x77, A: 0x88})
	img.SetRGBA(0, 1, color.RGBA{R: 0x99, G: 0xAA, B: 0xBB, A: 0xCC})
	img.SetRGBA(1, 1, color.RGBA{R: 0xDD, G: 0xEE, B: 0xFF, A: 0x11})

	want := []byte{
		0x33, 0x22, 0x11, 0x44, // top left, as B G R A
		0x77, 0x66, 0x55, 0x88, // top right
		0xBB, 0xAA, 0x99, 0xCC, // bottom left, on the second row
		0xFF, 0xEE, 0xDD, 0x11, // bottom right
	}

	pixels := make([]byte, len(want))
	writeDIBPixels(pixels, img)
	if !bytes.Equal(pixels, want) {
		t.Errorf("the DIB bits are % x, want % x", pixels, want)
	}

	// The DIB section hands over exactly size*size*4 bytes, so the writer must
	// stop there; anything past it would be memory it does not own.
	guarded := make([]byte, len(want)+8)
	writeDIBPixels(guarded, img)
	if !bytes.HasPrefix(guarded, want) || !bytes.Equal(guarded[len(want):], make([]byte, 8)) {
		t.Errorf("the DIB bits with room to spare are % x, want the pixels and nothing after them", guarded)
	}
}

// TestTheAndMaskIsAnEmptyBitmapOfPaddedRows covers the second bitmap
// CreateIconIndirect wants. Its rows are padded to a whole 32 bit word, so the
// row length steps at 32 and 64 rather than at the icon size, and every bit is
// clear: a set bit would punch a hole in the mark even though the shape is
// carried by the colour bitmap's alpha.
func TestTheAndMaskIsAnEmptyBitmapOfPaddedRows(t *testing.T) {
	for size, stride := range map[int]int{1: 4, 16: 4, 22: 4, 31: 4, 32: 4, 33: 8, 64: 8, 65: 12} {
		if got := andMaskStride(size); got != stride {
			t.Errorf("andMaskStride(%d) = %d, want %d", size, got, stride)
		}
		mask := andMask(size)
		if len(mask) != stride*size {
			t.Errorf("andMask(%d) is %d bytes, want one %d byte row per row of the icon", size, len(mask), stride)
			continue
		}
		if !bytes.Equal(mask, make([]byte, len(mask))) {
			t.Errorf("andMask(%d) has bits set, so the icon would be cut away where they are", size)
		}
	}
}
