// Package service polls the portal on a timer and signs back in when the
// session has been dropped.
package service

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"sdautologin/internal/config"
	"sdautologin/internal/logging"
	"sdautologin/internal/portal"
)

// State is the session state as shown to the user.
type State int

const (
	StateUnknown State = iota
	StateOnline
	StateOffline
	StateLoggingIn
	StateError
)

func (s State) String() string {
	switch s {
	case StateOnline:
		return "Online"
	case StateOffline:
		return "Offline"
	case StateLoggingIn:
		return "Signing in"
	case StateError:
		return "Error"
	default:
		return "Checking"
	}
}

// Code is the stable identifier of a state. It travels as the "state" field of
// the settings API and as the suffix of the i18n key "state.<code>", so the page
// and the tray label the same state the same way.
func (s State) Code() string {
	switch s {
	case StateOnline:
		return "online"
	case StateOffline:
		return "offline"
	case StateLoggingIn:
		return "loggingIn"
	case StateError:
		return "error"
	default:
		return "checking"
	}
}

// Snapshot is what the UI renders.
type Snapshot struct {
	State           State
	ClientIP        string
	Account         string
	OnlineSince     string
	Message         string
	LastLoginAt     string
	LastError       string
	InternetChecked bool
	InternetOK      bool

	// Entry names the configured account the session was signed in with, and
	// EntryIndex is its position in the login list, so the settings page can
	// mark the entry in use. EntryIndex is -1 while nothing has signed in.
	Entry      string
	EntryIndex int
	// Skipped lists the entries that are being left alone for a while: the
	// portal refused them for a reason time will not fix by itself.
	Skipped []Skipped
}

// Skipped is one entry of the login list that is not being tried, why, and how
// much of the wait is left.
type Skipped struct {
	Index int
	// Message is what the portal said, and RetCode is the number it answered
	// with, so the settings page can say the same thing in the language of its
	// own interface.
	Message  string
	RetCode  string
	RetryInS int
}

// cooldownPeriod is how long an entry the portal definitively refused is left
// alone. Submitting a wrong password every check interval would hammer the
// portal and fill the log for as long as the account stays broken, while a fix
// behind it (the password was changed, the account was topped up) is noticed
// when the wait is over, or at once when the settings are saved.
const cooldownPeriod = 10 * time.Minute

// rejection is a definitively refused attempt and the moment it may be tried
// again.
type rejection struct {
	message string
	retCode string
	index   int
	until   time.Time
}

// Service polls the portal and signs back in when the session is gone.
type Service struct {
	mu        sync.Mutex
	cfg       *config.Config
	client    *portal.Client
	listener  func(Snapshot)
	snap      Snapshot
	lastLogin string
	running   bool
	stopCh    chan struct{}
	triggerCh chan struct{}

	checks       int64
	attempts     int64
	failStreak   int64
	lastOnlineAt time.Time
	// activeIndex and activeEntry are the entry of the login list the session is
	// on, remembered so every later snapshot can name it rather than only the
	// round that signed in. activeIndex is -1 while nothing is known.
	activeIndex int
	activeEntry string
	// rejected holds the attempts the portal refused for a reason of its own,
	// keyed by config.Attempt.Key, so the check walks past a broken account
	// instead of submitting it again every interval.
	rejected map[string]rejection
}

func NewService(cfg *config.Config) *Service {
	return &Service{
		cfg:         cfg,
		client:      portal.NewClient(cfg),
		triggerCh:   make(chan struct{}, 1),
		rejected:    map[string]rejection{},
		activeIndex: -1,
		snap: Snapshot{
			State:      StateUnknown,
			Message:    "waiting for first check",
			EntryIndex: -1,
		},
	}
}

// SetListener registers the status callback. It runs on the worker goroutine.
func (s *Service) SetListener(f func(Snapshot)) {
	s.mu.Lock()
	s.listener = f
	s.mu.Unlock()
}

func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

func (s *Service) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	stop := make(chan struct{})
	s.stopCh = stop
	interval := time.Duration(s.cfg.CheckIntervalSeconds) * time.Second
	s.mu.Unlock()

	go s.loop(stop, interval)
	logging.Log.Info("service started: %s", s.cfg.Summary())
}

func (s *Service) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	s.mu.Unlock()
	checks, attempts := s.stats()
	logging.Log.Debug("service stopped (checks=%d, login attempts=%d)", checks, attempts)
}

func (s *Service) stats() (checks, attempts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checks, s.attempts
}

// Restart applies a new configuration without recreating the Service. The
// entries that were refused before are given another chance, since the settings
// just saved may be what was wrong with one of them.
func (s *Service) Restart(cfg *config.Config) {
	s.Stop()
	s.mu.Lock()
	s.cfg = cfg
	s.client = portal.NewClient(cfg)
	s.rejected = map[string]rejection{}
	s.mu.Unlock()
	s.Start()
}

// Trigger asks for an immediate check; it never blocks. An entry that was being
// skipped is tried again, since a check asked for now usually follows a fix.
func (s *Service) Trigger() {
	s.mu.Lock()
	s.rejected = map[string]rejection{}
	s.mu.Unlock()
	select {
	case s.triggerCh <- struct{}{}:
	default:
	}
}

func (s *Service) loop(stop <-chan struct{}, interval time.Duration) {
	s.tick()
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
			s.tick()
			timer.Reset(interval)
		case <-s.triggerCh:
			logging.Log.Debug("check requested out of schedule")
			s.tick()
			timer.Reset(interval)
		}
	}
}

func (s *Service) tick() {
	start := time.Now()
	cfg := s.cfg

	s.mu.Lock()
	s.checks++
	checks := s.checks
	prev := s.snap
	lastOnline := s.lastOnlineAt
	s.mu.Unlock()

	logging.Log.Debug("check #%d: querying portal, previous state %s", checks, prev.State)

	st := s.client.QueryStatus()
	logging.Log.Debug("check #%d: reachable=%v online=%v clientIp=%q detail=%q",
		checks, st.Reachable, st.Online, st.ClientIP, st.Message)

	snap := Snapshot{ClientIP: st.ClientIP, Account: st.Account, OnlineSince: st.OnlineSince}
	s.mu.Lock()
	snap.LastLoginAt = s.lastLogin
	snap.Entry, snap.EntryIndex = s.activeEntry, s.activeIndex
	s.mu.Unlock()

	offline := offlineSuffix(lastOnline)

	switch {
	case !st.Reachable:
		snap.State = StateError
		snap.Message = st.Message
		snap.LastError = st.Message
		if prev.State != StateError || prev.Message != st.Message {
			logging.Log.Warn("portal unreachable: %s", st.Message)
		} else {
			logging.Log.Debug("portal still unreachable: %s", st.Message)
		}

	case !st.Known:
		// The portal answered but the session state could not be read. Reading
		// that as a dropped session would fire a pointless login on every
		// transient failure, so skip the reconnect for this round.
		snap.State = StateError
		snap.Message = "session state unknown: " + st.Message
		snap.LastError = snap.Message
		if prev.State != StateError || prev.Message != snap.Message {
			logging.Log.Warn("cannot read the session state: %s, skipping the reconnect this round%s",
				st.Message, offline)
		} else {
			logging.Log.Debug("session state still unknown: %s", st.Message)
		}

	case st.Online:
		if cfg.CheckInternet {
			snap.InternetChecked = true
			snap.InternetOK = s.client.ProbeInternet()
		} else {
			snap.InternetOK = true
		}
		snap.State = StateOnline
		internetFailed := snap.InternetChecked && !snap.InternetOK
		if internetFailed {
			snap.Message = "portal online, Internet unreachable"
		}

		if prev.State != StateOnline {
			if !lastOnline.IsZero() {
				logging.Log.Info("session is back online after %s offline (account %s, client ip %s)",
					shortDuration(time.Since(lastOnline)), st.Account, st.ClientIP)
			} else {
				logging.Log.Info("session is online (account %s, client ip %s)", st.Account, st.ClientIP)
			}
		}
		if internetFailed {
			// Only on the transition, otherwise an unreachable probe host would
			// warn once per check.
			if prev.State != StateOnline || prev.InternetOK {
				logging.Log.Warn("portal reports online but no probe host answered without interception")
			} else {
				logging.Log.Debug("probe hosts still unreachable while the portal reports online")
			}
		}

		s.mu.Lock()
		s.lastOnlineAt = time.Now()
		s.failStreak = 0
		s.mu.Unlock()

		// The portal names the account this address is signed in with, so the
		// interface can say which entry of the list the session is on, including
		// one this program did not sign in itself.
		if st.Account != "" {
			if index, isp := cfg.Match(st.Account); index >= 0 {
				s.setActive(index, cfg.EntryLabel(index, isp))
			} else {
				s.setActive(-1, "")
			}
			snap.Entry, snap.EntryIndex = s.active()
		}

	case !cfg.AutoLogin:
		snap.State = StateOffline
		snap.Message = "auto login is disabled"
		if prev.State != StateOffline {
			logging.Log.Warn("session is down but auto login is disabled, staying offline%s", offline)
		}

	case !cfg.Configured():
		snap.State = StateError
		snap.Message = "no account configured, open the settings window"
		snap.LastError = snap.Message
		// Compared by message as well: the previous round may already have been
		// an Error for a different reason, which would hide this one.
		if prev.State != StateError || prev.Message != snap.Message {
			logging.Log.Warn("%s%s", snap.Message, offline)
		}

	default:
		snap = s.signIn(cfg, st, snap, offline)
	}

	// The entries that are being left alone go into every snapshot, whatever
	// this round ended up doing, so the page can mark them.
	snap.Skipped = s.skipped(time.Now())

	logging.Log.Debug("check #%d finished in %d ms: %s", checks, elapsedMS(start), DescribeSnapshot(snap))
	s.publish(snap)
}

// signIn walks the login list and submits the first attempt that is not being
// skipped.
//
// The two kinds of failure are handled differently. A refusal that blames the
// account moves on to the next entry; a busy portal, a timeout or an unreadable
// reply ends the round here, so that a temporary problem does not mark every
// account as broken at once.
func (s *Service) signIn(cfg *config.Config, st portal.Status, snap Snapshot, offline string) Snapshot {
	now := time.Now()
	s.pruneRejected(now)

	attempts := cfg.Attempts()
	ready := make([]config.Attempt, 0, len(attempts))
	for _, a := range attempts {
		if s.cooling(a) {
			continue
		}
		ready = append(ready, a)
	}
	if len(ready) == 0 {
		return s.allRefused(snap, now, offline)
	}

	for i, a := range ready {
		snap.State = StateLoggingIn
		snap.Message = "disconnected, signing in as " + a.Entry()
		s.publish(snap)

		s.mu.Lock()
		s.attempts++
		attempt := s.attempts
		s.mu.Unlock()

		logging.Log.Info("session is down, reconnect attempt #%d as %s (entry %d of %d, client ip %q)%s",
			attempt, a.Entry(), a.Index+1, len(cfg.Accounts), st.ClientIP, offline)

		attemptStart := time.Now()
		res := s.client.Login(st, a)
		if !res.Success {
			s.mu.Lock()
			s.failStreak++
			streak := s.failStreak
			s.mu.Unlock()

			if transient(res) {
				snap.State = StateError
				snap.LastError = res.Message
				snap.Message = "login failed: " + res.Message
				logging.Log.Warn("reconnect attempt #%d as %s rejected after %d ms: %s (ret_code=%s, raw %s)%s",
					attempt, a.Entry(), elapsedMS(attemptStart), res.Message, res.RetCode, portal.Truncate(res.Raw), offline)
				logging.Log.Warn("portal-side failure, so the rest of the list waits for the next check")
				logReconnectFailure(attempt, streak, cfg.CheckIntervalSeconds, offline)
				return snap
			}

			// The portal refused this account for a reason that waiting will not
			// fix, so it is left alone for a while and the next entry is tried.
			s.reject(a, res, now)
			logging.Log.Warn("reconnect attempt #%d as %s was refused: %s (ret_code=%s, raw %s)%s",
				attempt, a.Entry(), res.Message, res.RetCode, portal.Truncate(res.Raw), offline)
			if i+1 < len(ready) {
				logging.Log.Warn("entry %d is left alone for %s, moving on to the next account",
					a.Index+1, shortDuration(cooldownPeriod))
			} else {
				// Saying that it moves on when there is nothing left to move on
				// to would be the one line a single account install ever sees.
				logging.Log.Warn("entry %d is left alone for %s, and there is no entry after it",
					a.Index+1, shortDuration(cooldownPeriod))
			}

			snap.State = StateError
			snap.LastError = res.Message
			snap.Message = fmt.Sprintf("%s was refused: %s", a.Entry(), res.Message)
			continue
		}

		// The login endpoint can report success while the portal still shows
		// the client as offline, so confirm before declaring victory.
		verify := s.client.QueryStatus()
		if !verify.Online {
			s.mu.Lock()
			s.failStreak++
			streak := s.failStreak
			s.mu.Unlock()

			snap.State = StateError
			snap.Message = "portal accepted the login but still reports offline"
			snap.LastError = snap.Message
			logging.Log.Warn("reconnect attempt #%d as %s: the login was accepted but the follow-up query says offline (portal reachable=%v, detail %q), raw login reply %s",
				attempt, a.Entry(), verify.Reachable, verify.Message, portal.Truncate(res.Raw))
			logReconnectFailure(attempt, streak, cfg.CheckIntervalSeconds, offline)
			return snap
		}

		last := time.Now().Format("01-02 15:04:05")
		entry := a.Entry()
		s.mu.Lock()
		s.lastLogin = last
		s.failStreak = 0
		s.lastOnlineAt = time.Now()
		s.activeIndex, s.activeEntry = a.Index, entry
		s.mu.Unlock()

		snap.State = StateOnline
		snap.LastLoginAt = last
		snap.Account = verify.Account
		snap.ClientIP = verify.ClientIP
		snap.OnlineSince = verify.OnlineSince
		snap.Entry, snap.EntryIndex = entry, a.Index
		snap.Message = "signed in as " + entry + " at " + last
		logging.Log.Info("reconnect attempt #%d succeeded in %d ms as %s (entry %d of %d), account %s, client ip %s, session since %s",
			attempt, elapsedMS(attemptStart), entry, a.Index+1, len(cfg.Accounts),
			verify.Account, verify.ClientIP, verify.OnlineSince)
		return snap
	}

	return s.allRefused(snap, now, offline)
}

// allRefused is the snapshot for a round in which every entry is either refused
// or still waiting out a refusal. The refusals themselves were logged when they
// happened; this says what the next try is waiting for.
func (s *Service) allRefused(snap Snapshot, now time.Time, offline string) Snapshot {
	snap.State = StateError
	skipped := s.skipped(now)
	wait := shortDuration(s.shortestWait(now))
	if len(skipped) == 1 {
		// One entry is the common case, and its reason tells the user more than
		// the news that the list was walked.
		snap.Message = fmt.Sprintf("%s, trying again in %s", skipped[0].Message, wait)
	} else {
		snap.Message = fmt.Sprintf("every account was refused by the portal, trying again in %s", wait)
	}
	snap.LastError = snap.Message
	logging.Log.Debug("%s%s", snap.Message, offline)
	return snap
}

// transient reports whether a rejected login says something about the portal
// rather than about the account. Only the codes that name the account itself
// give up on an entry; an answer that does not, or one this program has never
// seen, is left for the next check instead of being held against the account.
func transient(res portal.LoginResult) bool {
	switch res.RetCode {
	case "1", // wrong account or password
		"7",       // authentication failed
		"9", "10": // logout failed or timed out
		return false
	}
	return true
}

// reject remembers that an entry was refused, and for how long it is to be left
// alone.
func (s *Service) reject(a config.Attempt, res portal.LoginResult, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected[a.Key()] = rejection{
		message: res.Message,
		retCode: res.RetCode,
		index:   a.Index,
		until:   now.Add(cooldownPeriod),
	}
}

// cooling reports whether an attempt is still waiting out a refusal.
func (s *Service) cooling(a config.Attempt) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.rejected[a.Key()]
	return ok
}

// pruneRejected forgets the refusals whose wait is over, so their entries are
// tried again.
func (s *Service) pruneRejected(now time.Time) {
	s.mu.Lock()
	expired := s.pruneRejectedLocked(nil, now)
	s.mu.Unlock()
	logExpired(expired)
}

// pruneRejectedLocked is pruneRejected with the lock already held. It drops the
// refusals whose wait is over and appends them to dst rather than making a slice
// of its own, so a caller that hands it a buffer with room to spare keeps this
// free of allocation while s.mu is held. A nil dst is fine, and is what the
// once-per-round caller passes: it then allocates only when a wait really ends.
func (s *Service) pruneRejectedLocked(dst []rejection, now time.Time) []rejection {
	for key, r := range s.rejected {
		if !now.Before(r.until) {
			delete(s.rejected, key)
			dst = append(dst, r)
		}
	}
	return dst
}

// logExpired reports the refusals whose wait just ended, in the one place the
// drop happens: a round that is offline prunes through signIn and one that is
// online through skipped, so a line in either of them alone would be missing
// from the other, which is exactly the path an operator watches to tell whether
// the cleanup ran. The caller runs this after releasing the lock.
func logExpired(expired []rejection) {
	for _, r := range expired {
		logging.Log.Info("entry %d is out of its %s cooldown and is tried again (it had been refused: %s, ret_code=%s)",
			r.index+1, shortDuration(cooldownPeriod), r.message, r.retCode)
	}
}

// skipped renders the entries that are being left alone, for the interface.
//
// The refusals whose wait is over are forgotten here first, so an entry a round
// that never reached signIn let expire is not reported as still being left alone
// with no wait left.
//
// The locked section allocates nothing: the two slices it fills are made in
// between from a count read under a short lock of its own, so a concurrent
// Snapshot is never queued behind the allocator. The count can only shrink
// between the two passes (the one caller that adds a refusal runs on this same
// goroutine, and the other two only empty the map), so the buffers always have
// room and the appends never grow.
func (s *Service) skipped(now time.Time) []Skipped {
	s.mu.Lock()
	size := len(s.rejected)
	s.mu.Unlock()

	out := make([]Skipped, 0, size)
	expired := make([]rejection, 0, size)

	s.mu.Lock()
	expired = s.pruneRejectedLocked(expired, now)
	for _, r := range s.rejected {
		retryIn := int(r.until.Sub(now).Round(time.Second).Seconds())
		if retryIn < 1 {
			// The entry is still being held back, so "0s" would read as "due
			// now" and a negative value as nonsense. Anything below one second
			// is rounded up to it.
			retryIn = 1
		}
		out = append(out, Skipped{
			Index:    r.index,
			Message:  r.message,
			RetCode:  r.retCode,
			RetryInS: retryIn,
		})
	}
	s.mu.Unlock()

	// slices.SortFunc rather than sort.Slice: the latter goes through
	// reflect.Swapper and allocates on every call, which sort.Slice's own
	// comparison func does not need.
	slices.SortFunc(out, func(a, b Skipped) int { return cmp.Compare(a.Index, b.Index) })
	logExpired(expired)
	return out
}

// shortestWait is how long until the first skipped entry may be tried again.
func (s *Service) shortestWait(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var shortest time.Duration
	for _, r := range s.rejected {
		if wait := r.until.Sub(now); shortest == 0 || wait < shortest {
			shortest = wait
		}
	}
	return shortest
}

// setActive remembers the entry the session is on, for every later snapshot.
func (s *Service) setActive(index int, entry string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeIndex, s.activeEntry = index, entry
}

// active is the entry the session is on.
func (s *Service) active() (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeEntry, s.activeIndex
}

func (s *Service) publish(snap Snapshot) {
	s.mu.Lock()
	s.snap = snap
	f := s.listener
	s.mu.Unlock()
	if f != nil {
		logging.Log.Debug("publishing status to ui: %s", DescribeSnapshot(snap))
		f(snap)
	}
}

// DescribeSnapshot renders a snapshot as one line for the log.
func DescribeSnapshot(s Snapshot) string {
	out := s.State.String()
	if s.Message != "" {
		out += ": " + s.Message
	}
	if s.ClientIP != "" {
		out += " (ip " + s.ClientIP + ")"
	}
	if s.Account != "" {
		out += " (account " + s.Account + ")"
	}
	// The entry is left out when the message already names it, as the line
	// written right after a successful sign-in does.
	if s.Entry != "" && !strings.Contains(s.Message, s.Entry) {
		out += fmt.Sprintf(" (entry %d: %s)", s.EntryIndex+1, s.Entry)
	}
	if len(s.Skipped) > 0 {
		for _, skip := range s.Skipped {
			out += fmt.Sprintf(" (entry %d skipped for %ds: %s)", skip.Index+1, skip.RetryInS, skip.Message)
		}
	}
	return out
}

func shortDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

// elapsedMS is how long a step took, in whole milliseconds, for the log lines.
func elapsedMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

// failStreakErrorLevel is the number of consecutive rejections after which the
// summary line is raised to ERROR, so a sustained outage stands out from a
// single transient failure.
const failStreakErrorLevel = 3

// offlineSuffix renders ", offline for 4m10s" once a previous online moment is
// known, which keeps the outage duration visible during a long failure run.
func offlineSuffix(lastOnline time.Time) string {
	if lastOnline.IsZero() {
		return ""
	}
	return ", offline for " + shortDuration(time.Since(lastOnline))
}

func logReconnectFailure(attempt, streak int64, retrySeconds int, offline string) {
	if streak >= failStreakErrorLevel {
		logging.Log.Error("reconnect attempt #%d failed, %d consecutive failures%s, next try in %ds",
			attempt, streak, offline, retrySeconds)
		return
	}
	logging.Log.Warn("reconnect attempt #%d failed, %d consecutive failure(s)%s, next try in %ds",
		attempt, streak, offline, retrySeconds)
}
