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

// Accent colors. AccentBrand is the fixed application identity, the deep crimson
// a Chinese university keeps its seal in; the others tint the mark by session
// state, so the tray icon reads at a glance.
var (
	AccentBrand   = color.RGBA{R: 0x8C, G: 0x1D, B: 0x2C, A: 0xFF}
	AccentOnline  = color.RGBA{R: 0x2E, G: 0xA0, B: 0x43, A: 0xFF}
	AccentOffline = color.RGBA{R: 0x7A, G: 0x86, B: 0x94, A: 0xFF}
	AccentUnknown = color.RGBA{R: 0x9A, G: 0xA3, B: 0xAD, A: 0xFF}
	AccentBusy    = color.RGBA{R: 0xE0, G: 0x8B, B: 0x1E, A: 0xFF}
	AccentError   = color.RGBA{R: 0xD1, G: 0x3A, B: 0x2E, A: 0xFF}
)

// Icon renders the mark: a round seal holding a padlock, tinted by session state.
// It is drawn from primitives at 4x and box filtered down, so no font or
// rasteriser is needed; the two rings keep the padlock readable at 16 px.
func Icon(size int, accent color.RGBA) *image.RGBA {
	const ss = 4
	n := size * ss
	canvas := image.NewRGBA(image.Rect(0, 0, n, n))

	fillCircle(canvas, n/2, n/2, px(float64(n)*0.500), darken(accent, 0.38))
	fillCircle(canvas, n/2, n/2, px(float64(n)*0.398), color.RGBA{})
	fillCircle(canvas, n/2, n/2, px(float64(n)*0.335), accent)

	white := color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	fillArc(canvas, n/2, px(float64(n)*0.465), px(float64(n)*0.165), px(float64(n)*0.085), 180, 360, white)
	fillRoundRect(canvas, px(float64(n)*0.290), px(float64(n)*0.465),
		px(float64(n)*0.710), px(float64(n)*0.735), px(float64(n)*0.055), white)
	fillCircle(canvas, n/2, px(float64(n)*0.585), px(float64(n)*0.052), accent)

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

// darken mixes a colour towards black. The ring of the seal takes a darker tone
// of the accent, so the mark stays one identity, tinted by the session state.
func darken(c color.RGBA, amount float64) color.RGBA {
	mix := func(v uint8) uint8 {
		out := float64(v) * (1 - amount)
		return uint8(math.Max(0, out))
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
