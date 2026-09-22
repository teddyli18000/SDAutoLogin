// Package brand draws the application mark. It only uses the standard image
// packages, so both the running program and the build time icon generator can
// produce the same artwork.
package brand

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Accent colors. AccentBrand is the fixed application identity, the others
// tint the tray icon to show the session state.
var (
	AccentBrand   = color.RGBA{R: 0x25, G: 0x63, B: 0xEB, A: 0xFF}
	AccentOnline  = color.RGBA{R: 0x2E, G: 0xA0, B: 0x43, A: 0xFF}
	AccentOffline = color.RGBA{R: 0x7A, G: 0x86, B: 0x94, A: 0xFF}
	AccentBusy    = color.RGBA{R: 0xE0, G: 0x8B, B: 0x1E, A: 0xFF}
	AccentError   = color.RGBA{R: 0xD1, G: 0x3A, B: 0x2E, A: 0xFF}
)

// Icon renders the mark: a rounded badge holding a padlock, tinted by session
// state. It is drawn from primitives at 4x and then box filtered down, so no
// font or rasteriser dependency is needed.
//
// The proportions are chosen for the 16 px tray icon: the shackle stays two
// pixels thick and the body keeps half a pixel of margin on each side, so the
// shape still reads as a padlock at the smallest size.
func Icon(size int, accent color.RGBA) *image.RGBA {
	const ss = 4
	n := size * ss
	canvas := image.NewRGBA(image.Rect(0, 0, n, n))

	fillRoundRect(canvas, 0, 0, n-1, n-1, px(float64(n)*0.24), accent)
	fillRoundRect(canvas, 0, 0, n-1, px(float64(n)*0.5), px(float64(n)*0.24), lighten(accent, 0.10))

	white := color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	// Shackle: the upper half of a ring, ending where the body starts.
	fillArc(canvas, n/2, px(float64(n)*0.46), px(float64(n)*0.24), px(float64(n)*0.12), 180, 360, white)
	// Body.
	fillRoundRect(canvas, px(float64(n)*0.28), px(float64(n)*0.46),
		px(float64(n)*0.72), px(float64(n)*0.82), px(float64(n)*0.08), white)
	// Keyhole punched back through in the accent color.
	fillCircle(canvas, n/2, px(float64(n)*0.63), px(float64(n)*0.065), accent)

	return downscale(canvas, size)
}

// px rounds a coordinate, so the shape is not skewed to one side by truncation
// towards zero for positive and negative values.
func px(v float64) int { return int(math.Round(v)) }

// PNG returns the mark encoded as PNG, the format the Linux and macOS tray
// backends want.
func PNG(size int, accent color.RGBA) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, Icon(size, accent)); err != nil {
		return nil
	}
	return buf.Bytes()
}

func lighten(c color.RGBA, amount float64) color.RGBA {
	mix := func(v uint8) uint8 {
		out := float64(v) + (255-float64(v))*amount
		return uint8(math.Min(255, out))
	}
	return color.RGBA{R: mix(c.R), G: mix(c.G), B: mix(c.B), A: c.A}
}

// downscale box filters an image by an integer factor, averaging in
// premultiplied space so transparent edges do not darken.
func downscale(src *image.RGBA, size int) *image.RGBA {
	n := src.Bounds().Dx()
	factor := n / size
	if factor < 1 {
		factor = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var sr, sg, sb, sa uint32
			for dy := 0; dy < factor; dy++ {
				for dx := 0; dx < factor; dx++ {
					c := src.RGBAAt(x*factor+dx, y*factor+dy)
					a := uint32(c.A)
					sr += uint32(c.R) * a
					sg += uint32(c.G) * a
					sb += uint32(c.B) * a
					sa += a
				}
			}
			if sa == 0 {
				continue
			}
			count := uint32(factor * factor)
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(sr / sa),
				G: uint8(sg / sa),
				B: uint8(sb / sa),
				A: uint8(sa / count),
			})
		}
	}
	return out
}

func fillRoundRect(img *image.RGBA, x0, y0, x1, y1, r int, c color.RGBA) {
	if r < 0 {
		r = 0
	}
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if insideRoundRect(x, y, x0, y0, x1, y1, r) {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func insideRoundRect(x, y, x0, y0, x1, y1, r int) bool {
	if x < x0+r && y < y0+r {
		return dist2(x, y, x0+r, y0+r) <= r*r
	}
	if x > x1-r && y < y0+r {
		return dist2(x, y, x1-r, y0+r) <= r*r
	}
	if x < x0+r && y > y1-r {
		return dist2(x, y, x0+r, y1-r) <= r*r
	}
	if x > x1-r && y > y1-r {
		return dist2(x, y, x1-r, y1-r) <= r*r
	}
	return true
}

func fillCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if dist2(x, y, cx, cy) <= r*r {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

// fillArc draws the part of a ring between two angles in degrees, measured
// clockwise from the positive x axis with y pointing down.
func fillArc(img *image.RGBA, cx, cy, rOuter, rInner int, fromDeg, toDeg float64, c color.RGBA) {
	for deg := fromDeg; deg <= toDeg; deg += 0.5 {
		rad := deg * math.Pi / 180
		for r := rInner; r <= rOuter; r++ {
			x := cx + px(math.Cos(rad)*float64(r))
			y := cy + px(math.Sin(rad)*float64(r))
			img.SetRGBA(x, y, c)
		}
	}
}

func dist2(x0, y0, x1, y1 int) int {
	dx, dy := x0-x1, y0-y1
	return dx*dx + dy*dy
}
