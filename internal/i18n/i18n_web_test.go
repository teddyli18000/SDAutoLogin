package i18n

import (
	"regexp"
	"strings"
	"testing"
)

// TestTheRefusalNamesTheWayOut pins what the prompt for a session the portal
// would not move is for: the card line has to name the buttons beside it, and the
// strip line has to name the service types and say the card is where to go.
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

// sendsToTheLog matches the words a wording would use to send the reader to the
// log. The address the settings page is served on carries a one time token, and a
// token that reaches a file on disk is not one any more, so no wording may point
// there.
var sendsToTheLog = regexp.MustCompile(`(?i)\blog\b|日志`)

// TestTheStalePageDoesNotSendTheReaderToTheLog holds the wording of a page whose
// session is gone. It used to name the log as the place to find the page's
// address, which is where the token it had just lost was written down.
func TestTheStalePageDoesNotSendTheReaderToTheLog(t *testing.T) {
	for _, lang := range []Lang{LangZH, LangEN} {
		stale := ALL(lang)["msg.stale"]
		if strings.TrimSpace(stale) == "" {
			t.Fatalf("the string table has no msg.stale in %s", lang)
		}
		if sendsToTheLog.MatchString(stale) {
			t.Errorf("%s: the wording still sends the reader to the log for the page's address: %q", lang, stale)
		}
	}
}
