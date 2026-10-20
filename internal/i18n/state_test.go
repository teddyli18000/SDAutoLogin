// Package i18n_test holds the checks that tie the string table to the rest of
// the program. They are an external test package because the check loop depends
// on i18n, so reaching for it from inside the package would be an import cycle.
package i18n_test

import (
	"strings"
	"testing"

	"sdautologin/internal/i18n"
	"sdautologin/internal/service"
)

// sessionStates are the states the check loop can publish.
func sessionStates() []service.State {
	return []service.State{
		service.StateUnknown,
		service.StateOnline,
		service.StateOffline,
		service.StateLoggingIn,
		service.StateError,
		service.StateNotKnown,
	}
}

func TestEverySessionStateHasWording(t *testing.T) {
	// The tray labels a state with the key "state.<code>", and L returns a key
	// it does not know unchanged, so a missing entry would show up in the
	// tooltip and the menu as "state.online".
	for _, lang := range []i18n.Lang{i18n.LangZH, i18n.LangEN} {
		all := i18n.ALL(lang)
		for _, st := range sessionStates() {
			if key := "state." + st.Code(); all[key] == "" {
				t.Errorf("language %s has no wording for %s (key %q)", lang, st, key)
			}
		}
	}
}

func TestEverySessionStateReachesItsWording(t *testing.T) {
	// The same lookup the tray performs, so a table that is present but not
	// reachable shows up here.
	t.Cleanup(func() { i18n.SetLang(i18n.LangAuto) })

	for _, lang := range []i18n.Lang{i18n.LangZH, i18n.LangEN} {
		i18n.SetLang(lang)
		for _, st := range sessionStates() {
			key := "state." + st.Code()
			got := i18n.L("state." + st.Code())
			if got == key {
				t.Errorf("L(%q) returned the key, so %s has no wording in %s", key, st, lang)
			}
			if strings.TrimSpace(got) == "" {
				t.Errorf("L(%q) = %q in %s", key, got, lang)
			}
		}
	}
}

func TestSessionStateWordingIsTranslated(t *testing.T) {
	// The interface is bilingual and L falls back to English, so a forgotten
	// translation would only be visible as one English line in a Chinese
	// interface.
	zh, en := i18n.ALL(i18n.LangZH), i18n.ALL(i18n.LangEN)

	for _, st := range sessionStates() {
		if key := "state." + st.Code(); zh[key] == en[key] {
			t.Errorf("%q reads %q in both languages", key, en[key])
		}
	}
}

func TestNoWordingForAStateThatDoesNotExist(t *testing.T) {
	// A key such as "state.loggedIn" is never looked up, so it would both go
	// unused and hide the entry it was meant to add.
	known := map[string]bool{}
	for _, st := range sessionStates() {
		known["state."+st.Code()] = true
	}

	for key := range i18n.ALL(i18n.LangEN) {
		if strings.HasPrefix(key, "state.") && !known[key] {
			t.Errorf("%q does not name a session state", key)
		}
	}
}
