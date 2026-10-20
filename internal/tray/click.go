package tray

import "time"

// clickDebounce remembers the click that last opened the settings page, so the
// second click of a double click does not open it twice. Windows sends down, up,
// double click, up for one double click, so both ups arrive and the settings
// page would be asked for on each. It is small and holds no lock because only
// the UI thread touches it.
//
// It lives in a file with no build tag so the arithmetic can be tested on the
// machine the CI job runs on, which has no Windows to click on.
type clickDebounce struct {
	last time.Time
}

// accept reports whether a click that arrived at now opens the settings page,
// and records it when it does. window is how long a second click still counts
// as part of the same double click.
//
// A refused click is not recorded, so a third click during a double click is
// measured against the first one and the reader is not locked out for a fresh
// window by each one. The first click of all compares against a zero time,
// which is far older than any window, so it always gets through.
func (d *clickDebounce) accept(now time.Time, window time.Duration) bool {
	if now.Sub(d.last) < window {
		return false
	}
	d.last = now
	return true
}

// doubleClickWindowFor is the interval to wait for the second click, given what
// GetDoubleClickTime answered. That call answers zero when it cannot say, and a
// zero interval would let every click through, so the classic 500 ms stands in
// for it rather than turning the de-bounce off.
func doubleClickWindowFor(ms uintptr) time.Duration {
	if ms == 0 {
		return 500 * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}
