//go:build (linux || darwin) && !notray

package tray

import (
	"strings"
	"testing"

	"sdautologin/internal/i18n"
	"sdautologin/internal/service"
)

// TestTheTooltipSaysWhatTheReaderNeedsAndNothingElse covers the lines the menu shows.
// The tray is built without a session bus here, which is what makes it testable: the
// tooltip is composed from the snapshot and reads nothing else. The portal's own message
// belongs next to a state that says something is wrong - on a session that is up it is
// the portal's chatter - and the rest is said only when it is true: a refusal to move the
// session, a release there is to move to, the address the portal sees this machine as,
// and the result of the internet check.
func TestTheTooltipSaysWhatTheReaderNeedsAndNothingElse(t *testing.T) {
	// The premise of every check below: the wording is in the shared table. A key that
	// is not there reads as itself, and a check comparing a key with a key would pass
	// whatever the tooltip did.
	for _, key := range []string{"app.name", "tray.status", "tray.switchRefused", "tray.clientIP", "tray.internetOk", "tray.internetFail"} {
		if i18n.L(key) == key {
			t.Fatalf("the wording table has no entry for %q, so this test cannot tell a tooltip from a key", key)
		}
	}

	var tray unixTray

	base := tray.tooltip(service.Snapshot{State: service.StateOnline})
	if !strings.HasPrefix(base, i18n.L("app.name")+"\n") {
		t.Errorf("the tooltip does not open with the name of the program: %q", base)
	}
	if !strings.Contains(base, i18n.L("state.online")) {
		t.Errorf("the tooltip does not say the session is up: %q", base)
	}
	if strings.Contains(base, i18n.L("state.offline")) {
		t.Errorf("the tooltip of a session that is up says it is down: %q", base)
	}

	failed := tray.tooltip(service.Snapshot{State: service.StateError, Message: "portal unreachable"})
	if !strings.Contains(failed, "portal unreachable") || !strings.Contains(failed, i18n.L("state.error")) {
		t.Errorf("the tooltip of a failed round does not carry what the portal said: %q", failed)
	}
	chatty := tray.tooltip(service.Snapshot{State: service.StateOnline, Message: "portal unreachable"})
	if strings.Contains(chatty, "portal unreachable") {
		t.Errorf("the tooltip of a session that is up carries a message that is not an error: %q", chatty)
	}

	refused := tray.tooltip(service.Snapshot{State: service.StateOnline, SwitchRefused: &service.Refusal{Message: "refused"}})
	if !strings.Contains(refused, i18n.L("tray.switchRefused")) {
		t.Errorf("a session the portal would not move is not said to be the reader's to move: %q", refused)
	}
	if strings.Contains(base, i18n.L("tray.switchRefused")) {
		t.Errorf("a tooltip with no refusal in it names one anyway: %q", base)
	}

	release := tray.tooltip(service.Snapshot{State: service.StateOnline, Update: &service.Update{Latest: "v9.9.9", Newer: true}})
	if !strings.Contains(release, "v9.9.9") {
		t.Errorf("a release there is to move to is not named: %q", release)
	}
	current := tray.tooltip(service.Snapshot{State: service.StateOnline, Update: &service.Update{Latest: "v9.9.9"}})
	if strings.Contains(current, "v9.9.9") {
		t.Errorf("a release that is not newer than this program is named anyway: %q", current)
	}

	seen := tray.tooltip(service.Snapshot{
		State:           service.StateOnline,
		ClientIP:        "10.9.1.99",
		InternetChecked: true,
		InternetOK:      true,
	})
	if !strings.Contains(seen, "10.9.1.99") || !strings.Contains(seen, i18n.L("tray.internetOk")) {
		t.Errorf("the tooltip does not say which address the portal sees and that the internet was reached: %q", seen)
	}
	if strings.Contains(seen, i18n.L("tray.internetFail")) {
		t.Errorf("the tooltip of a check that worked says the internet is unreachable: %q", seen)
	}

	unreachable := tray.tooltip(service.Snapshot{State: service.StateOnline, InternetChecked: true})
	if !strings.Contains(unreachable, i18n.L("tray.internetFail")) {
		t.Errorf("the tooltip does not say the internet check failed: %q", unreachable)
	}
	for _, said := range []string{i18n.L("tray.internetOk"), i18n.L("tray.internetFail")} {
		if strings.Contains(base, said) {
			t.Errorf("a check that has not run is reported as %q: %q", said, base)
		}
	}
}
