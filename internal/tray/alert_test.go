package tray

import (
	"strings"
	"testing"
	"time"

	"sdautologin/internal/service"
)

// errorRound is the sequence one failed check round publishes: the session is
// down, the program signs in again, and the sign-in fails.
var errorRound = []service.State{service.StateOffline, service.StateLoggingIn, service.StateError}

func TestAnOutageIsAnnouncedOnceAndThenRemindedEveryFewMinutes(t *testing.T) {
	start := time.Unix(1700000000, 0)
	var alert outageAlert

	if alert.note(service.Snapshot{State: errorRound[0]}, start) {
		t.Error("the round that found the session down was announced as a failure")
	}
	if alert.note(service.Snapshot{State: service.StateLoggingIn}, start) {
		t.Error("signing in was announced as a failure")
	}
	if !alert.note(service.Snapshot{State: service.StateError, Message: "portal unreachable"}, start) {
		t.Error("the first failed sign-in was not announced")
	}

	// The rounds after it are the same outage, however often they fail: the
	// LoggingIn in between must not make each one look like a new failure.
	for round := 1; round <= 15; round++ {
		at := start.Add(time.Duration(round) * 15 * time.Second)
		for _, st := range errorRound {
			if alert.note(service.Snapshot{State: st, Message: "portal unreachable"}, at) {
				t.Fatalf("round %d at %s was announced again", round, at.Sub(start))
			}
		}
	}

	// A long outage is still mentioned now and then, no more often than the
	// interval the alert keeps.
	reminders := 0
	for minute := 1; minute <= 30; minute++ {
		at := start.Add(time.Duration(minute) * time.Minute)
		for _, st := range errorRound {
			if alert.note(service.Snapshot{State: st, Message: "portal unreachable"}, at) {
				reminders++
			}
		}
	}
	if reminders != 6 {
		t.Errorf("a 30 minute outage was mentioned %d more times, want 6", reminders)
	}
}

func TestTheSessionComingBackEndsTheOutage(t *testing.T) {
	start := time.Unix(1700000000, 0)
	var alert outageAlert

	if !alert.note(service.Snapshot{State: service.StateError}, start) {
		t.Fatal("the first failure was not announced")
	}
	if alert.note(service.Snapshot{State: service.StateOnline}, start.Add(15*time.Second)) {
		t.Error("the session coming back was announced as a failure")
	}
	// The next failure is a new outage, and the reader is told about it at once
	// instead of waiting out the interval of the one before.
	if !alert.note(service.Snapshot{State: service.StateError}, start.Add(20*time.Second)) {
		t.Error("the failure after the session came back was not announced")
	}
}

func TestStatesOutsideTheOutageAreNotFailures(t *testing.T) {
	var alert outageAlert
	now := time.Unix(1700000000, 0)

	for _, st := range []service.State{service.StateUnknown, service.StateOffline, service.StateLoggingIn, service.StateOnline} {
		if alert.note(service.Snapshot{State: st}, now) {
			t.Errorf("state %s was announced as a failure", st)
		}
	}
}

// A release worth moving to is a line of its own in the tooltip, and nothing is
// said while there is nothing newer or while the check has not run.
func TestTheTooltipNamesAReleaseWorthMovingTo(t *testing.T) {
	newer := &service.Update{Newer: true, Latest: "v2.0.0", Message: "x"}
	got := updateLine(service.Snapshot{Update: newer})
	if !strings.Contains(got, "v2.0.0") {
		t.Errorf("the line is %q, and it does not name the release", got)
	}
	for _, s := range []service.Snapshot{
		{},
		{Update: nil},
		{Update: &service.Update{Newer: false, Latest: "v2.0.0"}},
	} {
		if line := updateLine(s); line != "" {
			t.Errorf("a snapshot with nothing newer produced %q", line)
		}
	}
}
