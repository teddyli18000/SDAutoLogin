package tray

import (
	"strings"
	"testing"

	"sdautologin/internal/service"
)

// TestEveryStateHasWordsOfItsOwn covers the name of a state in the menu and in the
// tooltip. The wording comes from the shared table by a key built from the state code, so
// a state whose key was misspelled or left out would put a key like "state.loggingIn" in
// front of the reader, and a state that fell through to another one's key would say the
// session is in a state it is not.
func TestEveryStateHasWordsOfItsOwn(t *testing.T) {
	states := []service.State{
		service.StateUnknown,
		service.StateOnline,
		service.StateOffline,
		service.StateLoggingIn,
		service.StateError,
		service.StateNotKnown,
	}

	seen := map[string]service.State{}
	for _, st := range states {
		label := stateLabel(st)
		if label == "" || strings.HasPrefix(label, "state.") {
			t.Errorf("stateLabel(%v) = %q, which is the key rather than words", st, label)
			continue
		}
		if other, ok := seen[label]; ok {
			t.Errorf("%v and %v are both named %q", other, st, label)
		}
		seen[label] = st
	}
	if len(seen) != len(states) {
		t.Errorf("%d states are named with %d names, so one of them is read as another", len(states), len(seen))
	}
}
