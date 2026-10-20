package tray

import "image"

// bitmapInfoHeader matches BITMAPINFOHEADER, the 40 byte header Win32 puts in
// front of a device independent bitmap. The field order and widths are the ones
// the OS reads, so they cannot be rearranged.
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

const (
	dibRGBColors = 0
	biRGB        = 0
)

// dibHeader is the header of the top-down 32bpp DIB an icon is copied through.
// The negative height is what makes it top-down: without it the first row of the
// buffer would be the bottom of the mark and the icon would be upside down.
func dibHeader(size int) bitmapInfoHeader {
	return bitmapInfoHeader{
		Size:        40,
		Width:       int32(size),
		Height:      -int32(size), // negative height: rows are stored top-down
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}
}

// writeDIBPixels fills the bits of that DIB from the drawn mark. A DIB stores
// each pixel as BGRA and a 32bpp row is exactly size*4 bytes, so there is no
// padding to skip; the row order comes from the negative height above.
func writeDIBPixels(pixels []byte, img *image.RGBA) {
	size := img.Bounds().Dx()
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
}

// andMaskStride is the byte length of one row of the AND mask CreateBitmap is
// given. Its rows are padded to a whole number of 32 bit words, as every bitmap
// Win32 hands around is, so a 16 px icon has a 4 byte row while its colour
// pixels need 64.
func andMaskStride(size int) int {
	return ((size + 31) / 32) * 4
}

// andMask is that mask, every bit clear. A 32bpp icon carries its shape in the
// alpha channel; the mask is only there because CreateIconIndirect requires one,
// and a set bit would punch a hole through the mark.
func andMask(size int) []byte {
	return make([]byte, andMaskStride(size)*size)
}
