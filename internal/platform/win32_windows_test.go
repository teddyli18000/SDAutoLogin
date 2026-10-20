//go:build windows

package platform

import (
	"syscall"
	"testing"
	"unsafe"
)

// TestTheWideStringHelperAlwaysHandsTheAPIAUsableBuffer covers the conversion
// every wide character call in this package goes through. Win32 reads the buffer
// as NUL terminated UTF-16 and has no length argument, so a buffer that is not
// terminated is read past its end, and a nil pointer is a fault inside the call.
func TestTheWideStringHelperAlwaysHandsTheAPIAUsableBuffer(t *testing.T) {
	const text = "sdautologin"
	p := UTF16Ptr(text)
	if p == nil {
		t.Fatal("UTF16Ptr returned nil for a string it can represent")
	}
	if got := syscall.UTF16ToString(unsafe.Slice(p, len(text)+1)); got != text {
		t.Errorf("UTF16Ptr made %q, want %q", got, text)
	}

	// A Go string may hold a NUL, and the API would read only up to it, so the
	// helper substitutes the empty string instead of the truncated one.
	q := UTF16Ptr("a\x00b")
	if q == nil {
		t.Fatal("UTF16Ptr returned nil for a string the wide API cannot represent")
	}
	if got := syscall.UTF16ToString(unsafe.Slice(q, 1)); got != "" {
		t.Errorf("a string with a NUL in it became %q, want the empty one", got)
	}
}

// TestShellExecuteIsToldToShowTheWindowNormally covers the show command handed
// to ShellExecuteW. It is the only thing that decides whether the settings page
// appears at all: SW_HIDE would open it invisibly.
func TestShellExecuteIsToldToShowTheWindowNormally(t *testing.T) {
	if swShowNormal != 1 {
		t.Errorf("swShowNormal is %d, want the 1 of SW_SHOWNORMAL", swShowNormal)
	}
}
