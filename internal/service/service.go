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
	// UsedISP is the service type of that entry the portal says the session is
	// on: the suffix the entry is submitted with, empty for the campus network
	// itself. The page names the one the session is on with it, rather than
	// every type the entry may be used with, which is the difference between
	// "the campus network" and "one of these two".
	UsedISP string
	// Skipped lists the entries that are being left alone for a while: the
	// portal refused them for a reason time will not fix by itself.
	Skipped []Skipped
	// PreferredISP is the service type of that entry the session should be on
	// when it is on another one, which is the first one the entry names, and
	// MoveToPreferred says whether there is anything to move it onto at all.
	// The page shows that service type with a button that signs in again, for a
	// reader who would rather move the session than wait for the next run.
	//
	// The two are told apart because the service type to move the session onto
	// may be the campus network itself, which is an empty suffix: the value
	// alone would say the same thing as "nothing to do".
	PreferredISP    string
	MoveToPreferred bool
	// SwitchRefused is what the portal answered when it would not sign in again
	// to move the session, or nil when no such sign-in is outstanding: none was
	// tried, the one that was tried worked, or the session is already where its
	// entry puts first. It is what says the session is being left as it is and
	// that moving it is now up to a reader.
	SwitchRefused *Refusal
}

// Refusal is what the portal answered when it would not do something, kept so
// the settings page can say it in the language of its own interface.
type Refusal struct {
	Message string
	RetCode string
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

// confirmTries is how many times the session is read back after a login the
// portal accepted.
//
// The login endpoint and the online list are written a moment apart. A portal
// answers a login with "authentication successful" and, in the same second,
// answers a query with "the online data is empty", because the session is not in
// the list yet. One read is therefore not an answer to the question "did it
// work", and taking it for one reported logins that had in fact succeeded as
// failures, down to the log line saying the portal accepted the login and still
// reported offline. Reading it back over the next second and a half costs
// nothing, since this happens only after a login the portal has already
// accepted.
const confirmTries = 3

// confirmDelay is how long those reads are spaced. It is a variable so that a
// test which has to go through this wait does not spend it.
var confirmDelay = 750 * time.Millisecond

// switchRetryPeriod is how long a move onto the service type an entry puts first
// is left alone after the portal refused one.
//
// A refusal is not the end of it: a portal that will not sign in again while a
// session is up may be that way about one service type and not another, or a
// session signed in by hand may be dropped and taken over later, so the move is
// asked about again. Asking every check would submit a login every fifteen
// seconds against a session the portal is happy with, which is why the period is
// this long, and it is a variable so a test does not have to spend it.
var switchRetryPeriod = time.Hour

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
	// done is closed by the loop as it returns, so a restart can wait for the
	// loop it just stopped before it hands the new configuration over.
	done      chan struct{}
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
	// switchAskedAt is when this session was last signed in again to move it onto
	// the service type its entry puts first, and switchRefused is what the portal
	// answered when it would not. A portal that will not sign in while a session
	// is up is not asked again until switchRetryPeriod has passed, and its answer
	// is kept so the page and the tray can say the session is being left as it is.
	// Both are cleared for a new session and when the settings are saved.
	switchAskedAt time.Time
	switchRefused *Refusal
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
	done := make(chan struct{})
	s.stopCh = stop
	s.done = done
	cfg := s.cfg
	s.mu.Unlock()

	go s.loop(stop, done, time.Duration(cfg.CheckIntervalSeconds)*time.Second)
	logging.Log.Info("service started: %s", cfg.Summary())
}

// Stop ends the check loop and waits for it to have finished. Waiting is what
// keeps a restart from running two loops at once: a loop that is in the middle of
// a check would otherwise carry on with the configuration and the client the
// restart has just replaced, so both of them would be talking to the portal at
// the same time, and the one that finished last would publish its own view of a
// configuration that is no longer in use.
//
// It must not be called from the loop itself, since it waits for that loop.
func (s *Service) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	done := s.done
	s.mu.Unlock()

	// Waited for outside the lock: the loop takes it again on its way out. The
	// wait is measured, since a check that is in the middle of talking to the
	// portal is the whole reason this waits at all: a stop that returns at once
	// is a loop that had nothing in flight.
	asked := time.Now()
	<-done

	checks, attempts := s.stats()
	logging.Log.Debug("service stopped (checks=%d, login attempts=%d, the loop ended %s after it was asked to)",
		checks, attempts, time.Since(asked).Round(time.Millisecond))
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
	logging.Log.Debug("restarting the check loop on new settings: %s", cfg.Summary())
	s.Stop()
	s.mu.Lock()
	s.cfg = cfg
	s.client = portal.NewClient(cfg)
	s.rejected = map[string]rejection{}
	// The settings may be what was wrong: a service type was added, the order
	// was changed, or the option that moves a session onto the first one was
	// turned on. A session that was left alone is asked about again at once.
	s.switchAskedAt = time.Time{}
	s.switchRefused = nil
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

func (s *Service) loop(stop <-chan struct{}, done chan struct{}, interval time.Duration) {
	defer close(done)
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
		// A session that is up on another service type of the same entry is
		// signed in again as the first one, when the configuration asks for it
		// and when this is the round that first saw it. See switchToPreferred.
		if a, ok := s.preferredPending(cfg, st); ok {
			snap = s.switchToPreferred(cfg, st, snap, a)
			break
		}

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

		snap = s.attribute(cfg, st, snap)

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

	// A session is being signed in from scratch, so whatever was tried to move
	// the one before it does not apply to this one.
	s.mu.Lock()
	s.switchAskedAt = time.Time{}
	s.switchRefused = nil
	s.mu.Unlock()

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
		// the client as offline, so confirm before declaring victory. See
		// confirmLogin for why that is asked more than once.
		verify := s.confirmLogin()
		if !verify.Online {
			if !verify.Known {
				// The portal could not be asked whether the login took, so
				// nothing is known about it. That is not a failure, and calling
				// it one would be a warning the next check has to take back:
				// the session is left as "signing in" and the next check
				// settles it, which is the same thing a status that cannot be
				// read does on the way in.
				snap.State = StateLoggingIn
				snap.Message = "signed in as " + a.Entry() + ", not confirmed yet"
				logging.Log.Warn("reconnect attempt #%d as %s: the login was accepted and the session could not be read back within %s (%s), the next check settles it, raw login reply %s",
					attempt, a.Entry(), shortDuration(time.Duration(confirmTries-1)*confirmDelay),
					verify.Message, portal.Truncate(res.Raw))
				return snap
			}

			s.mu.Lock()
			s.failStreak++
			streak := s.failStreak
			s.mu.Unlock()

			snap.State = StateError
			snap.Message = "portal accepted the login but still reports offline"
			snap.LastError = snap.Message
			logging.Log.Warn("reconnect attempt #%d as %s: the login was accepted and the session is not in the portal's online list after %d reads over %s (%s), raw login reply %s",
				attempt, a.Entry(), confirmTries,
				shortDuration(time.Duration(confirmTries-1)*confirmDelay), verify.Message, portal.Truncate(res.Raw))
			logReconnectFailure(attempt, streak, cfg.CheckIntervalSeconds, offline)
			return snap
		}

		logging.Log.Info("reconnect attempt #%d succeeded in %d ms as %s (entry %d of %d), account %s, client ip %s, session since %s",
			attempt, elapsedMS(attemptStart), a.Entry(), a.Index+1, len(cfg.Accounts),
			verify.Account, verify.ClientIP, verify.OnlineSince)
		return s.signedIn(snap, a, verify, "signed in as")
	}

	return s.allRefused(snap, now, offline)
}

// confirmLogin reads the session back after a login the portal accepted, and
// hands back what the portal said. See confirmTries.
func (s *Service) confirmLogin() portal.Status {
	verify := s.client.QueryStatus()
	for i := 1; i < confirmTries && !verify.Online; i++ {
		time.Sleep(confirmDelay)
		verify = s.client.QueryStatus()
	}
	return verify
}

// signedIn is what a login the portal confirmed leaves behind: the snapshot the
// interface is shown, and the session facts every later snapshot is built from.
// verb is how the log and the page word it, "signed in as" for a sign-in and
// "switched to" for a session that was moved onto another service type.
func (s *Service) signedIn(snap Snapshot, a config.Attempt, verify portal.Status, verb string) Snapshot {
	last := time.Now().Format("01-02 15:04:05")
	entry := a.Entry()
	s.mu.Lock()
	s.lastLogin = last
	s.failStreak = 0
	s.lastOnlineAt = time.Now()
	s.activeIndex, s.activeEntry = a.Index, entry
	// The session is a new one, so a refusal to move the one before it is neither
	// reported any more nor worth waiting on.
	s.switchRefused = nil
	s.switchAskedAt = time.Time{}
	s.mu.Unlock()

	snap.State = StateOnline
	snap.LastLoginAt = last
	snap.Account = verify.Account
	snap.ClientIP = verify.ClientIP
	snap.OnlineSince = verify.OnlineSince
	snap.Entry, snap.EntryIndex = entry, a.Index
	// The login went out with this service type appended to the account, and the
	// portal echoes what it was given, so this is the type the session is on. The
	// switch is left with nothing to do: it has just been made, or was asked
	// once for this session already.
	snap.UsedISP = a.ISP
	snap.MoveToPreferred = false
	snap.Message = verb + " " + entry + " at " + last
	return snap
}

// attribute records which entry of the list the portal says the session is on,
// so the interface can mark it, including a session this program did not sign in
// itself. It also says which service type that entry puts first when the session
// is not on it, which is what the page offers the button for.
func (s *Service) attribute(cfg *config.Config, st portal.Status, snap Snapshot) Snapshot {
	if st.Account == "" {
		return snap
	}
	if index, isp := cfg.Match(st.Account); index >= 0 {
		s.setActive(index, cfg.EntryLabel(index, isp))
		snap.UsedISP = isp
	} else {
		s.setActive(-1, "")
		snap.UsedISP = ""
	}
	snap.Entry, snap.EntryIndex = s.active()

	snap.PreferredISP = ""
	snap.MoveToPreferred = false
	a, ok := cfg.PreferredFor(st.Account)
	if !ok {
		// The session is where its entry puts first, so a refusal to move it is
		// no longer news: it is forgotten rather than kept to be reported the
		// next time the session is somewhere else. The wait is forgotten with
		// it, since there is nothing being waited on.
		s.mu.Lock()
		s.switchRefused = nil
		s.switchAskedAt = time.Time{}
		s.mu.Unlock()
		return snap
	}
	snap.PreferredISP = a.ISP
	snap.MoveToPreferred = true
	s.mu.Lock()
	refused := s.switchRefused
	s.mu.Unlock()
	snap.SwitchRefused = refused
	return snap
}

// preferredPending reports the attempt a session that is already up should be
// moved onto, and false when there is nothing to do.
//
// A move is only ever inside one entry of the list: a session on an account the
// list names, on a service type other than the first one of that entry. Signing
// in as another entry's account would replace a session that works with one that
// the order of the list says nothing about - the list is the failover order for
// signing in, not a ranking of who should be online.
func (s *Service) preferredPending(cfg *config.Config, st portal.Status) (config.Attempt, bool) {
	if !cfg.SwitchToPreferredService {
		return config.Attempt{}, false
	}
	if !cfg.AutoLogin {
		// Moving a session is a sign-in, and one switch already answers whether
		// this program signs in on its own. A reader who turned that off is left
		// to the button on the page, which is offered either way.
		return config.Attempt{}, false
	}
	s.mu.Lock()
	askedAt := s.switchAskedAt
	s.mu.Unlock()
	if wait := switchRetryPeriod - time.Since(askedAt); !askedAt.IsZero() && wait > 0 {
		// Asked not long ago, and the portal would not move the session then. It
		// is asked again when the wait is over: the answer may change, and a
		// login every check against a session the portal is happy with is not
		// something to do in the meantime.
		return config.Attempt{}, false
	}
	a, ok := cfg.PreferredFor(st.Account)
	if !ok {
		return config.Attempt{}, false
	}
	if s.cooling(a) {
		// The first service type is being left alone for a while, so the session
		// stays where it is rather than moving onto something that was refused.
		return config.Attempt{}, false
	}
	return a, true
}

// switchToPreferred signs in again while the session is up, to move it onto the
// service type its entry puts first. The portal keeps one session per client
// address, so this either replaces the session or is refused; either way the
// session is left working, and a refusal is not treated as the account being
// broken: the entry has to stay usable for when the session does drop.
func (s *Service) switchToPreferred(cfg *config.Config, st portal.Status, snap Snapshot, a config.Attempt) Snapshot {
	s.mu.Lock()
	s.switchAskedAt = time.Now()
	s.attempts++
	attempt := s.attempts
	s.mu.Unlock()

	logging.Log.Info("the session is on %s, which is not the service type its entry puts first, signing in again as %s (attempt #%d)",
		st.Account, a.Entry(), attempt)

	start := time.Now()
	res := s.client.Login(st, a)
	if !res.Success {
		// What the portal said is kept, not just logged: the session stays as it
		// is either way, and the page is where a reader is told so.
		s.setSwitchRefused(&Refusal{Message: res.Message, RetCode: res.RetCode})
		logging.Log.Warn("the portal would not sign in again while the session is up: %s (ret_code=%s, raw %s); the session is left as it is and asked about again in %s",
			res.Message, res.RetCode, portal.Truncate(res.Raw), shortDuration(switchRetryPeriod))
		message := "the portal would not sign in again while the session is up"
		if stays := s.entryLabel(cfg, st); stays != "" {
			message += ", the session stays as " + stays
		}
		return s.attribute(cfg, st, s.stillOnline(snap, st, message))
	}

	verify := s.confirmLogin()
	if !verify.Online {
		logging.Log.Warn("the portal accepted the second sign-in as %s and does not list the session as online (%s), raw login reply %s; the next check settles it",
			a.Entry(), verify.Message, portal.Truncate(res.Raw))
		return s.attribute(cfg, st, s.stillOnline(snap, st,
			"signed in again as "+a.Entry()+", not confirmed yet"))
	}

	logging.Log.Info("signed in again in %d ms, the session is now %s, since %s",
		elapsedMS(start), a.Entry(), verify.OnlineSince)
	return s.signedIn(snap, a, verify, "switched to")
}

// setSwitchRefused remembers what the portal answered when it would not sign in
// again to move the session, which is what the page and the tray report.
func (s *Service) setSwitchRefused(r *Refusal) {
	s.mu.Lock()
	s.switchRefused = r
	s.mu.Unlock()
}

// entryLabel names the entry the portal says the session is on, for a message a
// reader has to make sense of. It is empty when the account names no entry.
func (s *Service) entryLabel(cfg *config.Config, st portal.Status) string {
	index, isp := cfg.Match(st.Account)
	if index < 0 {
		return ""
	}
	return cfg.EntryLabel(index, isp)
}

// stillOnline is the snapshot for a round that found the session up and did not
// move it, either because the portal would not sign in again or because that is
// not known yet. The session is still a working one, so it is reported as online
// with the reason written into the message.
func (s *Service) stillOnline(snap Snapshot, st portal.Status, message string) Snapshot {
	s.mu.Lock()
	s.lastOnlineAt = time.Now()
	s.failStreak = 0
	s.mu.Unlock()

	snap.State = StateOnline
	snap.Message = message
	snap.OnlineSince = st.OnlineSince
	return snap
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
