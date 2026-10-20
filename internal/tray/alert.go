package tray

import (
	"time"

	"sdautologin/internal/service"
)

// errorNotifyEvery is the shortest gap between two notifications about the same
// outage. A check round runs every few seconds, and one notification per round
// would turn the tray into an alarm nobody reads.
const errorNotifyEvery = 5 * time.Minute

// outageAlert decides which failed rounds the reader is told about. A failed
// round publishes LoggingIn before it fails again, so the snapshot alone cannot
// tell an outage still running from one just started.
type outageAlert struct {
	// announced is set while the rounds are failing and cleared when the session
	// comes back, since only then is a further failure a new outage.
	announced bool
	// at is when the reader was last told about the outage that is running.
	at time.Time
}

// note records one snapshot and reports whether the reader is to be told about a
// failure now.
func (a *outageAlert) note(s service.Snapshot, now time.Time) bool {
	switch {
	case s.State == service.StateOnline:
		// The session is up, so the next failure is a new outage and is announced
		// without waiting for the repeat interval.
		a.announced = false
		return false
	case s.State != service.StateError:
		// Offline, LoggingIn and the rest are the rounds of an outage passing
		// through: neither the start nor the end of one.
		return false
	case !a.announced:
		a.announced = true
		a.at = now
		return true
	case now.Sub(a.at) >= errorNotifyEvery:
		// Still failing, so the reader is reminded that the session is down, at
		// most once every few minutes.
		a.at = now
		return true
	}
	return false
}
