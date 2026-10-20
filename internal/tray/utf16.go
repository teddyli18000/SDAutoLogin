package tray

import "unicode/utf16"

// putUTF16 copies s into a fixed size UTF-16 buffer, always NUL terminated.
//
// The Win32 fields it fills are fixed arrays, and the tray rewrites them as the
// state changes, so the buffer is cleared first: a shorter tooltip must not be
// followed by the tail of the one before it. It carries no build tag so the
// truncation rule can be tested off Windows.
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
