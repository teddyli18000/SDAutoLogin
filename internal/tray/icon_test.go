package tray

import (
	"image/color"
	"testing"

	"sdautologin/internal/brand"
	"sdautologin/internal/service"
)

// TestTheAccentColourSaysWhatTheStateIs covers the mark next to the icon: it is the one
// thing about the session a reader sees without opening the menu, so every state has the
// colour that says it. Green is a session that is up, red is a sign-in that failed, and
// the two greys are apart on purpose - a session that is down is not the same as one
// that has never been checked - while a state this program does not know is not coloured
// as a good one whatever it is.
func TestTheAccentColourSaysWhatTheStateIs(t *testing.T) {
	cases := []struct {
		name  string
		state service.State
		want  color.RGBA
	}{
		{"a session that is up", service.StateOnline, brand.AccentOnline},
		{"a session that is down", service.StateOffline, brand.AccentOffline},
		{"a sign-in in progress", service.StateLoggingIn, brand.AccentBusy},
		{"a sign-in that failed", service.StateError, brand.AccentError},
		{"a portal that answered without saying anything usable", service.StateNotKnown, brand.AccentUnknown},
		{"before the first round has answered", service.StateUnknown, brand.AccentUnknown},
		{"a state this program does not know", service.State(99), brand.AccentUnknown},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := iconAccent(c.state); got != c.want {
				t.Errorf("iconAccent(%v) = %v, want %v", c.state, got, c.want)
			}
		})
	}

	// Two states that are not the same must not be the colour of a session that is up:
	// the mark is read at a glance, and a green one is read as "nothing to do".
	for _, st := range []service.State{service.StateOffline, service.StateError, service.StateNotKnown} {
		if iconAccent(st) == brand.AccentOnline {
			t.Errorf("the mark for %v is the colour of a session that is up", st)
		}
	}
}
