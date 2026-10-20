package tray

import (
	"testing"
	"time"
)

// debounceWindow is the double click interval the tests work with. The real one
// comes from Windows and is read at run time, so nothing here may depend on its
// value.
const debounceWindow = 400 * time.Millisecond

// TestTheFirstClickOpensTheSettingsPage covers the click that has no earlier one
// to compare against. Its timestamp is the zero time, which is far older than
// any interval, and a reader's first click has to get through.
func TestTheFirstClickOpensTheSettingsPage(t *testing.T) {
	var clicks clickDebounce
	if !clicks.accept(time.Unix(1700000000, 0), debounceWindow) {
		t.Error("the first click was refused, so the settings page could never be opened")
	}
}

// TestTheSecondClickOfADoubleClickIsRefused covers why the de-bounce exists.
// Windows sends down, up, double click, up for one double click, so without it
// the settings page would be asked for twice and two tabs would open.
func TestTheSecondClickOfADoubleClickIsRefused(t *testing.T) {
	var clicks clickDebounce
	first := time.Unix(1700000000, 0)
	if !clicks.accept(first, debounceWindow) {
		t.Fatal("the first click was refused")
	}
	if clicks.accept(first.Add(debounceWindow-time.Millisecond), debounceWindow) {
		t.Error("the second click of a double click opened the settings page again")
	}
}

// TestAClickAfterTheWindowOpensThePageAgain covers the far side of the interval:
// a click one whole interval later is a click of its own, which is where Windows
// itself stops looking for a second one.
func TestAClickAfterTheWindowOpensThePageAgain(t *testing.T) {
	var clicks clickDebounce
	first := time.Unix(1700000000, 0)
	if !clicks.accept(first, debounceWindow) {
		t.Fatal("the first click was refused")
	}
	if !clicks.accept(first.Add(debounceWindow), debounceWindow) {
		t.Error("a click one whole interval after the first was still treated as its double click")
	}
}

// TestARefusedClickDoesNotMoveTheWindow covers what a refused click does to the
// next one. It has to leave the recorded click alone: if it were recorded, a
// reader clicking three times in a row would be refused for a fresh interval by
// each one and the page would never open again.
func TestARefusedClickDoesNotMoveTheWindow(t *testing.T) {
	var clicks clickDebounce
	first := time.Unix(1700000000, 0)
	if !clicks.accept(first, debounceWindow) {
		t.Fatal("the first click was refused")
	}
	if clicks.accept(first.Add(debounceWindow/2), debounceWindow) {
		t.Fatal("a click inside the window was accepted")
	}
	if !clicks.accept(first.Add(debounceWindow), debounceWindow) {
		t.Error("the click after the window was refused, so the refused one moved the window")
	}
}

// TestAnIntervalTheSystemCouldNotReportStillDebounces covers the fallback for
// GetDoubleClickTime answering zero. A zero interval would let every click
// through, so the classic 500 ms stands in; any other answer is used as it is.
func TestAnIntervalTheSystemCouldNotReportStillDebounces(t *testing.T) {
	if got := doubleClickWindowFor(0); got != 500*time.Millisecond {
		t.Errorf("an answer of 0 gives a %s interval, want the classic 500ms", got)
	}
	if got := doubleClickWindowFor(250); got != 250*time.Millisecond {
		t.Errorf("an answer of 250 gives %s, want 250ms", got)
	}
}
