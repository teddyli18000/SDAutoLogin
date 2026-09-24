package i18n

import (
	"strings"
	"testing"
)

// TestTheRefusalNamesTheWayOut pins what the prompt for a session the portal
// would not move is for. The session is up and the program has stopped trying,
// so the line on the card is worth nothing unless it names the buttons beside it,
// and the line in the strip of readings is worth nothing unless it says which
// service type the session is stuck on and that the card is where to go. Reword
// them freely, but keep both of those in them.
func TestTheRefusalNamesTheWayOut(t *testing.T) {
	for _, lang := range []Lang{LangZH, LangEN} {
		table := ALL(lang)

		card := table["acct.preferred.refused"]
		for _, key := range []string{"acct.preferred.btn", "acct.preferred.portal"} {
			if label := table[key]; !strings.Contains(card, label) {
				t.Errorf("%s: the card line does not name %q, so it does not say which button to press: %q",
					lang, label, card)
			}
		}

		strip := table["status.switchRefused"]
		for _, want := range []string{"{0}", "{1}"} {
			if !strings.Contains(strip, want) {
				t.Errorf("%s: the strip line does not name %s, so it does not say which service types are in question: %q",
					lang, want, strip)
			}
		}
	}
}
