package tray

import (
	"slices"
	"testing"
	"unicode/utf16"
)

// TestTheFixedBufferIsClearedAndTerminated covers the field the tray rewrites as
// the state changes. Win32 reads it as a NUL terminated wide string, so the text
// has to end in a zero and a shorter tooltip must not be followed by the tail of
// the longer one before it.
func TestTheFixedBufferIsClearedAndTerminated(t *testing.T) {
	dst := make([]uint16, 8)
	for i := range dst {
		dst[i] = 0xFFFF // what a longer tooltip would have left behind
	}
	putUTF16(dst, "ab")

	want := []uint16{'a', 'b', 0, 0, 0, 0, 0, 0}
	if !slices.Equal(dst, want) {
		t.Errorf("the buffer holds %#v, want %#v", dst, want)
	}
}

// TestAnEmptyTooltipClearsTheField covers the state with nothing to say: the
// field has to become empty rather than keep the tooltip of the state before it.
func TestAnEmptyTooltipClearsTheField(t *testing.T) {
	dst := make([]uint16, 8)
	putUTF16(dst, "signed in")
	putUTF16(dst, "")

	if !slices.Equal(dst, make([]uint16, 8)) {
		t.Errorf("an empty tooltip left %#v in the field", dst)
	}
}

// TestALongerTooltipIsCutToWhatTheFieldHolds covers the truncation rule. The
// fields are fixed arrays, so a title or a message longer than the array is cut
// to fit, and the room for the terminator is kept: filling the last cell would
// leave the API reading past the end of the struct.
func TestALongerTooltipIsCutToWhatTheFieldHolds(t *testing.T) {
	dst := make([]uint16, 4)
	putUTF16(dst, "abcdef")

	want := []uint16{'a', 'b', 'c', 0}
	if !slices.Equal(dst, want) {
		t.Errorf("a long string became %#v, want %#v", dst, want)
	}
}

// TestACharacterOutsideTheBasicPlaneSurvivesWhole covers the width the rule
// counts in. The buffer is counted in UTF-16 code units, and the common Chinese
// tooltips are one unit per character while a character outside the basic plane
// is two; a cut in the middle of such a pair leaves a lone surrogate, which the
// tray draws as a replacement character.
func TestACharacterOutsideTheBasicPlaneSurvivesWhole(t *testing.T) {
	const smile = '\U0001F600'
	units := utf16.Encode([]rune{smile})
	if len(units) != 2 {
		t.Fatalf("the test character is %d code units, want the 2 of a surrogate pair", len(units))
	}

	dst := make([]uint16, len(units)+1)
	putUTF16(dst, string(smile)+string(smile))

	want := append(append([]uint16{}, units...), 0)
	if !slices.Equal(dst, want) {
		t.Errorf("the buffer holds %#v, want the first character whole as %#v", dst, want)
	}
}
