package brand

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// drawnSizes are the sizes the program actually asks for: the Windows tray
// follows the system small icon metric, which is 16 px on a classic display and
// larger when it is scaled, the Linux and macOS tray puts up 22 px, and the
// build time icon generator draws the larger ones for the executables.
var drawnSizes = []int{16, 22, 24, 32, 48, 64}

// accents are every tint the mark is drawn with, the fixed identity plus one per
// session state.
var accents = []color.RGBA{AccentBrand, AccentOnline, AccentOffline, AccentUnknown, AccentBusy, AccentError}

// TestTheMarkIsDrawnAtTheSizeThatWasAsked covers the contract of the whole
// package: a caller that asks for 22 px gets a 22 px square at the origin, in a
// buffer the tray toolkits can read straight through.
func TestTheMarkIsDrawnAtTheSizeThatWasAsked(t *testing.T) {
	for _, size := range drawnSizes {
		img := Icon(size, AccentBrand)
		if got, want := img.Bounds(), image.Rect(0, 0, size, size); got != want {
			t.Errorf("Icon(%d) has bounds %v, want %v", size, got, want)
			continue
		}
		// A toolkit converts the buffer to its own surface without asking how
		// long it is, so it has to be the tight size*size*4 bytes of an RGBA
		// square, not a padded image.
		if got, want := len(img.Pix), 4*size*size; got != want {
			t.Errorf("Icon(%d) has %d bytes of pixels, want %d", size, got, want)
		}
		if got, want := img.Stride, 4*size; got != want {
			t.Errorf("Icon(%d) has a stride of %d bytes, want %d", size, got, want)
		}
	}
}

// TestTheSealIsRoundAndFillsItsBox covers the two things a reader sees first:
// the mark is a disc, so its corners are empty and it sits on any panel colour,
// and it fills the box, so it does not look shrunken next to the other icons in
// the tray.
func TestTheSealIsRoundAndFillsItsBox(t *testing.T) {
	for _, size := range drawnSizes {
		img := Icon(size, AccentBrand)
		corners := [][2]int{{0, 0}, {size - 1, 0}, {0, size - 1}, {size - 1, size - 1}}
		for _, c := range corners {
			if got := img.RGBAAt(c[0], c[1]); got.A != 0 {
				t.Errorf("Icon(%d): the corner at (%d,%d) is %v, want it transparent so the seal reads as a circle", size, c[0], c[1], got)
			}
		}
		if img.RGBAAt(size/2, 0).A == 0 {
			t.Errorf("Icon(%d): nothing is drawn where the seal meets the middle of the top edge", size)
		}
		if img.RGBAAt(size/2, size-1).A == 0 {
			t.Errorf("Icon(%d): nothing is drawn where the seal meets the middle of the bottom edge", size)
		}
	}
}

// TestNoColourIsLeftUnderAFullyTransparentPixel covers how the mark composites:
// a pixel nothing was drawn in must be empty, all four channels, or its colour
// would show as a fringe around the seal on a panel of a different colour. The
// downscale only keeps this true because it averages in premultiplied space.
func TestNoColourIsLeftUnderAFullyTransparentPixel(t *testing.T) {
	for _, size := range drawnSizes {
		img := Icon(size, AccentBrand)
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				c := img.RGBAAt(x, y)
				if c.A != 0 || c == (color.RGBA{}) {
					continue
				}
				t.Fatalf("Icon(%d): the transparent pixel (%d,%d) still carries colour %v", size, x, y, c)
			}
		}
	}
}

// TestTheEdgeIsSoftenedOnlyInSupersampledSteps covers the granularity of the
// anti-aliased rim. The mark is drawn at four times the asked size and box
// filtered down, so an edge pixel is a whole number of sixteenths of coverage
// and nothing in between: an alpha of any other value means the edge is being
// averaged by something other than that filter, and the rim would be blurred or
// stepped where it used to be neither.
func TestTheEdgeIsSoftenedOnlyInSupersampledSteps(t *testing.T) {
	const subpixels = 4 * 4
	allowed := map[uint8]bool{0: true, 255: true}
	for covered := 1; covered < subpixels; covered++ {
		allowed[uint8(255*covered/subpixels)] = true
	}

	for _, size := range drawnSizes {
		img := Icon(size, AccentBrand)
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				if a := img.RGBAAt(x, y).A; !allowed[a] {
					t.Fatalf("Icon(%d): the pixel (%d,%d) has alpha %d, which no whole number of the 16 subpixels per pixel can produce", size, x, y, a)
				}
			}
		}
	}
}

// TestTheSealTakesTheAccentAndThePadlockStaysWhite covers what the two colours
// of the mark are for: the accent is the session state, so the state has to reach
// the drawing exactly, and the padlock is white so it reads on top of it.
func TestTheSealTakesTheAccentAndThePadlockStaysWhite(t *testing.T) {
	white := color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	for _, size := range []int{16, 22, 32} {
		for _, accent := range accents {
			img := Icon(size, accent)
			var seal, padlock bool
			for i := 0; i+3 < len(img.Pix); i += 4 {
				switch c := (color.RGBA{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}); c {
				case accent:
					seal = true
				case white:
					padlock = true
				}
			}
			if !seal {
				t.Errorf("Icon(%d, %v) has no pixel of the accent itself, so the mark is not tinted by the state", size, accent)
			}
			if !padlock {
				t.Errorf("Icon(%d, %v) has no white pixel, so the padlock does not stand out on the seal", size, accent)
			}
		}
	}
}

// TestTheRingIsADarkerToneOfTheAccent covers the depth of the seal. The outer
// ring is the same identity as the disc, so it may only be a darker version of
// the same colour: a ring of the accent itself would flatten the mark, and one
// of another hue would read as a different icon.
func TestTheRingIsADarkerToneOfTheAccent(t *testing.T) {
	for _, size := range []int{16, 22, 32, 64} {
		img := Icon(size, AccentBrand)

		// The ring is the only thing the mark draws on the middle column above
		// the centre, so the first pixel down that column that has any coverage
		// at all carries the ring's colour.
		var ring color.RGBA
		found := false
		for y := 0; y < size/2 && !found; y++ {
			if c := img.RGBAAt(size/2, y); c.A != 0 {
				ring, found = c, true
			}
		}
		if !found {
			t.Fatalf("Icon(%d): the middle column is transparent above the centre, so the seal has no ring", size)
		}
		if ring.R > AccentBrand.R || ring.G > AccentBrand.G || ring.B > AccentBrand.B {
			t.Errorf("Icon(%d): the ring %v is not darker than the accent %v", size, ring, AccentBrand)
		}
		if ring.R == AccentBrand.R && ring.G == AccentBrand.G && ring.B == AccentBrand.B {
			t.Errorf("Icon(%d): the ring is the accent itself, so the seal has no depth", size)
		}
		// That first pixel is only partly covered, being where the circle cuts
		// the rim. Its colour still has to be the ring's own: the downscale
		// averages in premultiplied space, so the transparent neighbours outside
		// the seal lower the coverage without darkening what is left of it. A
		// rim mixed in straight RGB would come out blackened here, which is the
		// halo that gives the trick away.
		if want := darken(AccentBrand, 0.38); ring.R != want.R || ring.G != want.G || ring.B != want.B {
			t.Errorf("Icon(%d): the partly covered rim is %v, want the ring's own colour %v", size, ring, want)
		}
	}
}

// TestEachSessionStateTintsTheMarkDifferently covers the reason the accent is an
// argument at all: the tray tells the states apart by colour and nothing else,
// so two states that drew the same mark would be indistinguishable.
func TestEachSessionStateTintsTheMarkDifferently(t *testing.T) {
	seen := make(map[string]color.RGBA, len(accents))
	for _, accent := range accents {
		key := string(Icon(22, accent).Pix)
		if other, twice := seen[key]; twice {
			t.Errorf("the accents %v and %v draw the same mark, so two session states would look alike in the tray", other, accent)
			continue
		}
		seen[key] = accent
	}
}

// TestTheSameCallDrawsTheSameMark covers what the tray relies on across a
// refresh: the icon is redrawn on every state change, and a drawing that varied
// between calls would make it flicker between two shapes of the same state.
func TestTheSameCallDrawsTheSameMark(t *testing.T) {
	for _, size := range drawnSizes {
		if first, second := Icon(size, AccentOnline), Icon(size, AccentOnline); !bytes.Equal(first.Pix, second.Pix) {
			t.Errorf("Icon(%d) drew two different marks", size)
		}
		if first, second := PNG(size, AccentOnline), PNG(size, AccentOnline); !bytes.Equal(first, second) {
			t.Errorf("PNG(%d) encoded two different images", size)
		}
	}
}

// TestThePNGIsAnImageOfTheAskedSize covers what the Linux and macOS trays hand
// to their toolkit: the encoded mark has to decode to the same square, with the
// alpha channel intact, since a transparent corner is what keeps it round there.
func TestThePNGIsAnImageOfTheAskedSize(t *testing.T) {
	for _, size := range drawnSizes {
		data := PNG(size, AccentOnline)
		if len(data) == 0 {
			t.Fatalf("PNG(%d) is empty", size)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("PNG(%d) does not decode: %v", size, err)
		}
		if got, want := img.Bounds(), image.Rect(0, 0, size, size); got != want {
			t.Errorf("PNG(%d) decodes to %v, want %v", size, got, want)
		}
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Errorf("PNG(%d): the top left corner came back with alpha %d, want it transparent", size, a)
		}
	}
}
