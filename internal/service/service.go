// Package service polls the portal on a timer and signs back in when the
// session has been dropped.
package service

import (
	"cmp"
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"time"

	"sdautologin/internal/config"
	"sdautologin/internal/logging"
	"sdautologin/internal/portal"
	"sdautologin/internal/update"
)

// State is the session state as shown to the user.
type State int

const (
	StateUnknown State = iota
	StateOnline
	StateOffline
	StateLoggingIn
	StateError
	// StateNotKnown is the portal answering without saying anything usable about
	// this client. The session is neither known up nor known down, so no sign-in
	// is submitted, and the state is its own: a portal that answers 502 for a
	// minute is not a broken session, and the tray colours the two differently.
	StateNotKnown
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
	case StateNotKnown:
		return "State unknown"
	default:
		return "Checking"
	}
}

// Code is the stable identifier of a state: the "state" field of the settings
// API and the suffix of the i18n key "state.<code>", so the page and the tray
// label one state one way.
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
	case StateNotKnown:
		return "unknown"
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
	// CheckedAt is when this round read the portal, as UTC in RFC 3339. The page
	// polls faster than the loop runs and a round can take a while on a slow
	// portal, so without it a reader cannot tell a state that is current from one
	// that was measured minutes ago.
	CheckedAt string

	// Entry names the account the session was signed in with and EntryIndex is
	// its position in the login list, so the page can mark the entry in use.
	// EntryIndex is -1 while nothing has signed in.
	Entry      string
	EntryIndex int
	// UsedISP is the service type of that entry the portal says the session is
	// on: the suffix the entry was submitted with, empty for the campus network
	// itself.
	UsedISP string
	// Skipped lists the entries being left alone for a while: the portal refused
	// them for a reason time will not fix.
	Skipped []Skipped
	// PreferredISP is the first service type its entry names, the one the session
	// should be on. MoveToPreferred is a field of its own because the campus
	// network is an empty suffix, so the value alone cannot say "nothing to do".
	PreferredISP    string
	MoveToPreferred bool
	// SwitchRefused is what the portal answered when it would not sign in again
	// to move the session, or nil when no such sign-in is outstanding: none was
	// tried, the try worked, or the session is where its entry puts it first.
	SwitchRefused *Refusal
	// Update is what the last look for a newer release found, or nil while the
	// check is off or has not run yet. It is separate from the session: the tray
	// and the page show both, and a release page that cannot be reached touches
	// nothing else here.
	Update *Update
}

// Update is what a look at the release page found, and what was done about it.
// The fields answer the same questions the update command prints.
type Update struct {
	// CheckedAt is when the forge was asked, as UTC in RFC 3339, the same form
	// the session reading above uses so the page reads both one way.
	CheckedAt string
	// Message is the one line to show a reader: what was found, or what was
	// installed.
	Message string
	// Current and Latest are the versions that were compared, and Channel is
	// which releases the look was allowed to take. Latest is empty when nothing
	// is newer.
	Current string
	Latest  string
	Channel string
	Newer   bool
	// Signed is whether this build can check what it downloads, and Installable
	// whether this copy could put the release in place; Reason says why not.
	Signed      bool
	Installable bool
	Reason      string
	// Installed is the program file a release was written over, empty when
	// nothing was installed.
	Installed string
}

// Updater is the look for a newer release, and the install of one this copy can
// take. The repository address, the version this build carries and the program
// file to replace all live in internal/app, which imports this package, so the
// implementation is handed in from there. A test hands in its own.
type Updater interface {
	// Check looks for a newer release on ch and reports what it found.
	Check(ctx context.Context, ch update.Channel) (update.Check, error)
	// Install puts a release in place and returns the path it wrote.
	Install(ctx context.Context, rel update.Release) (string, error)
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
	// Message is what the portal said and RetCode is the number it answered
	// with, so the page can say the same thing in its own interface language.
	Message  string
	RetCode  string
	RetryInS int
}

// cooldownPeriod is how long an entry the portal definitively refused is left
// alone: long enough not to hammer the portal with a wrong password, short
// enough to pick up a fix. A save or a manual check clears it at once.
const cooldownPeriod = 10 * time.Minute

// confirmTries is how many times the session is read back after a login the
// portal accepted. The login endpoint and the online list are written a moment
// apart, so one read is not an answer to "did it work". See confirmLogin.
const confirmTries = 3

// confirmDelay is how long those reads are spaced. It is a variable so that a
// test which has to go through this wait does not spend it.
var confirmDelay = 750 * time.Millisecond

// switchRetryPeriod is how long a move onto the service type an entry puts
// first is left alone after the portal refused one. The refusal is not final;
// asking every check would submit a login against a working session.
var switchRetryPeriod = time.Hour

// loginSpacing is the least wait between two logins submitted in one round, and
// loginSpacingJitter the spread added on top of it at random. A round used to
// submit every account of the list back to back, one HTTP round trip apart, so a
// portal that answered "wrong account or password" to each entry was handed the
// whole list in milliseconds. The first account of a round goes out at once, and
// an installation with one account never waits at all.
const (
	loginSpacing       = 300 * time.Millisecond
	loginSpacingJitter = 200 * time.Millisecond
)

// maxBackoff is the longest a run of failures that say something about the portal
// makes the check loop wait, and backoffJitterFraction is the share of that wait
// added on top of it at random. The wait doubles after each failure of the run, so
// a portal that is down for an hour is asked a few times instead of every check,
// and clients of one campus do not all come back at the same moment.
const (
	maxBackoff            = 5 * time.Minute
	backoffJitterFraction = 0.25
)

// stopWaitTimeout is how long Stop waits for the check that is running, and
// updateStopTimeout how long it waits for a look at the release page once that
// look has been cancelled. A check in flight cannot be aborted from here, the
// portal client having no context to cancel, so Stop gives up on it and says so
// rather than being held open behind a slow portal or a download. Both are
// variables so a test does not have to spend them.
var (
	stopWaitTimeout   = 30 * time.Second
	updateStopTimeout = 5 * time.Second
)

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
	mu sync.Mutex
	// lifeMu serializes starting, stopping and restarting the loop, which mu
	// cannot do: Stop releases mu while it waits for the loop to finish, and the
	// loop takes it again on its way out. Two callers would otherwise interleave
	// their swaps.
	lifeMu    sync.Mutex
	cfg       *config.Config
	client    *portal.Client
	listener  func(Snapshot)
	snap      Snapshot
	lastLogin string
	running   bool
	stopCh    chan struct{}
	// done is closed by the loop as it returns, so a restart can wait for the
	// loop it stopped before it hands the new configuration over.
	done      chan struct{}
	triggerCh chan struct{}

	checks       int64
	attempts     int64
	failStreak   int64
	lastOnlineAt time.Time
	// lastKnownAt is the last round the portal said something usable about this
	// client, whether or not the session was up.
	lastKnownAt time.Time
	// downSince is when the session was last read as not signed in while the
	// portal could be read at all, so a recovery is measured from it. Measuring
	// from the last online reading reported an outage for every round in which
	// the answer could not be read, and those say nothing about the session.
	downSince time.Time
	// activeIndex and activeEntry are the entry of the login list the session is
	// on. Later snapshots name the entry from here. activeIndex is -1 while
	// nothing is known.
	activeIndex int
	activeEntry string
	// rejected holds the attempts the portal refused for a reason of its own,
	// keyed by config.Attempt.Key, so the check walks past a broken account
	// instead of submitting it every interval.
	rejected map[string]rejection
	// switchAskedAt is when this session was last signed in again to move it onto
	// the service type its entry puts first. The portal is not asked again until
	// switchRetryPeriod has passed, and switchRefused holds the answer it gave.
	switchAskedAt time.Time
	switchRefused *Refusal
	// updates is the look for a newer release, or nil when no one handed one in
	// and the forge is left alone.
	updates Updater
	// nextUpdateAt is the earliest moment the forge may be asked again, set when
	// a look starts so a release page that cannot be reached is not asked on
	// every check either.
	nextUpdateAt time.Time
	// update is the result of the last look, kept here because every snapshot is
	// built from scratch and the tray and the page show it until the next look.
	update *Update
	// updateWG tracks the look for a newer release. It runs on its own, so a
	// download of a few megabytes cannot hold up the next look at the portal,
	// which is the thing this program is for.
	updateWG sync.WaitGroup
	// updateCtx bounds that look: Stop cancels it, so a download in flight does
	// not decide how long the program takes to close. startLocked gives every run
	// a context of its own, the one a run used being cancelled as that run ends.
	updateCtx     context.Context
	cancelUpdates context.CancelFunc
	// sleep is the pause a round makes between two accounts of the login list. It
	// is a field so a test can have the waits reported to it instead of spending
	// them.
	sleep func(time.Duration)
	// transientStreak counts the checks in a row that ended on the portal's own
	// trouble, and backoffWait is the wait the round that just finished scheduled
	// for the next check. Both are per-round: the loop reads them once the round
	// has ended.
	transientStreak int
	backoffWait     time.Duration
}

func NewService(cfg *config.Config) *Service {
	// A look at the release page can be asked for before the loop is started, so
	// the first context is made here as well as in startLocked.
	updateCtx, cancelUpdates := context.WithCancel(context.Background())
	return &Service{
		cfg:           cfg,
		client:        portal.NewClient(cfg),
		triggerCh:     make(chan struct{}, 1),
		rejected:      map[string]rejection{},
		activeIndex:   -1,
		sleep:         time.Sleep,
		updateCtx:     updateCtx,
		cancelUpdates: cancelUpdates,
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

// SetUpdater hands the loop the look for a newer release. It is called once,
// before the loop starts; without it the forge is never asked.
func (s *Service) SetUpdater(u Updater) {
	s.mu.Lock()
	s.updates = u
	s.mu.Unlock()
}

func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

func (s *Service) Start() {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	s.startLocked()
}

func (s *Service) startLocked() {
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
	// A context of its own for the looks at the release page: the one the previous
	// run used was cancelled as that run was stopped, and a cancelled context
	// would refuse every look this run makes.
	s.updateCtx, s.cancelUpdates = context.WithCancel(context.Background())
	cfg := s.cfg
	s.mu.Unlock()

	go s.loop(stop, done, time.Duration(cfg.CheckIntervalSeconds)*time.Second)
	logging.Log.Info("service started: %s", cfg.Summary())
}

// Stop ends the check loop and waits for it to have finished: a loop caught in
// the middle of a check would otherwise carry on with the configuration and
// client the restart replaced. It must not be called from the loop itself.
func (s *Service) Stop() {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	s.stopLocked()
}

func (s *Service) stopLocked() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	done := s.done
	cancelUpdates := s.cancelUpdates
	s.mu.Unlock()

	// Waited for outside the lock: the loop takes it again on its way out. The
	// wait is bounded, because a check in flight cannot be aborted from here: the
	// portal client has no context to cancel, and a portal that answers slowly
	// must not hold the program open behind it. A loop given up on ends by itself,
	// as soon as the check it is in finishes, and nothing on that path writes to a
	// channel of a run that is over.
	asked := time.Now()
	if !waitClosed(done, stopWaitTimeout) {
		logging.Log.Warn("the check loop is still in a check %s after it was asked to stop; closing anyway, it ends on its own",
			shortDuration(time.Since(asked)))
	}
	// And the look for a newer release, which is not the loop's to end: it is a
	// download, and one may be running when the program is stopped. It is
	// cancelled first, so the wait for it is short.
	if cancelUpdates != nil {
		cancelUpdates()
	}
	if !waitGroup(&s.updateWG, updateStopTimeout) {
		logging.Log.Warn("a look for a newer release did not end within %s of being cancelled; leaving it to finish on its own",
			shortDuration(updateStopTimeout))
	}

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
// this call installs may be what was wrong with one of them.
func (s *Service) Restart(cfg *config.Config) {
	logging.Log.Debug("restarting the check loop on new settings: %s", cfg.Summary())
	// The whole of it is one critical section. The loop being replaced has to be
	// gone before the configuration is swapped and its replacement is started,
	// and a second caller must not get in between those three steps: it would
	// start a loop that the first caller then overwrites the configuration of.
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()

	s.stopLocked()
	s.mu.Lock()
	s.cfg = cfg
	s.client = portal.NewClient(cfg)
	s.rejected = map[string]rejection{}
	// The settings may be what was wrong: a service type was added, the order
	// changed, the option turned on. A session that was left alone is asked about
	// again at once.
	s.switchAskedAt = time.Time{}
	s.switchRefused = nil
	s.mu.Unlock()
	s.startLocked()
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
	timer := time.NewTimer(s.waitBeforeNextCheck(interval))
	defer timer.Stop()
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
			s.tick()
			timer.Reset(s.waitBeforeNextCheck(interval))
		case <-s.triggerCh:
			logging.Log.Debug("check requested out of schedule")
			s.tick()
			timer.Reset(s.waitBeforeNextCheck(interval))
		}
	}
}

func (s *Service) tick() {
	start := time.Now()

	s.mu.Lock()
	s.checks++
	checks := s.checks
	// The wait is per-round, and the loop reads what this round ends up
	// scheduling; the run of failed rounds is cleared by a round that read the
	// portal, further down.
	s.backoffWait = 0
	// Taken under the lock and used from here on: a round that Stop gave up waiting
	// for can still be running while a restart installs the next configuration and
	// client, and it has to finish on the pair it started with.
	cfg, client := s.cfg, s.client
	prev := s.snap
	lastOnline := s.lastOnlineAt
	lastKnown := s.lastKnownAt
	down := s.downSince
	s.mu.Unlock()

	base := time.Duration(cfg.CheckIntervalSeconds) * time.Second
	logging.Log.Debug("check #%d: querying portal, previous state %s", checks, prev.State)

	st := client.QueryStatus()
	logging.Log.Debug("check #%d: reachable=%v online=%v clientIp=%q detail=%q",
		checks, st.Reachable, st.Online, st.ClientIP, st.Message)

	if st.Known {
		s.mu.Lock()
		s.lastKnownAt = time.Now()
		s.mu.Unlock()
	}

	snap := Snapshot{
		ClientIP: st.ClientIP, Account: st.Account, OnlineSince: st.OnlineSince,
		// RFC 3339 in UTC, not the local display form the other times use,
		// because the page subtracts it from its own clock to say how old the
		// reading is, and only this form parses the same in every browser.
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.mu.Lock()
	snap.LastLoginAt = s.lastLogin
	snap.Entry, snap.EntryIndex = s.activeEntry, s.activeIndex
	snap.Update = s.update
	s.mu.Unlock()

	offline := offlineSuffix(lastOnline)
	// Not the same as offline: a portal that answers 502, or does not answer in
	// time, says nothing about whether the session is up.
	unread := notKnownSuffix(lastKnown)

	switch {
	case !st.Reachable:
		// The portal is not answering, which says nothing about the accounts: this
		// is the fault the wait before the next check grows from.
		streak, wait := s.noteTransientFailure(base)
		snap.State = StateError
		snap.Message = st.Message
		snap.LastError = st.Message
		if prev.State != StateError || prev.Message != st.Message {
			logging.Log.Warn("portal unreachable: %s", st.Message)
		} else {
			logging.Log.Debug("portal still unreachable: %s", st.Message)
		}
		logBackoff("the portal has been unreachable for", streak, wait, base)

	case !st.Known:
		// The portal answered but the state could not be read. Taking that for a
		// dropped session would fire a pointless login on every transient failure,
		// so this round does not reconnect, and the session is not reported as
		// broken either: the answer is what could not be read.
		streak, wait := s.noteTransientFailure(base)
		snap.State = StateNotKnown
		snap.Message = "session state unknown: " + st.Message
		snap.LastError = snap.Message
		if prev.State != StateNotKnown || prev.Message != snap.Message {
			logging.Log.Warn("cannot read the session state: %s, skipping the reconnect this round%s",
				st.Message, unread)
		} else {
			logging.Log.Debug("session state still unknown: %s", st.Message)
		}
		logBackoff("the portal has answered nothing usable for", streak, wait, base)

	case st.Online:
		// A session up on another service type of the same entry is signed in
		// again as the first one, when the configuration asks for it. See
		// switchToPreferred.
		if a, ok := s.preferredPending(cfg, st); ok {
			snap = s.switchToPreferred(cfg, client, st, snap, a)
			break
		}

		if cfg.CheckInternet {
			snap.InternetChecked = true
			snap.InternetOK = client.ProbeInternet()
		} else {
			snap.InternetOK = true
		}
		snap.State = StateOnline
		internetFailed := snap.InternetChecked && !snap.InternetOK
		if internetFailed {
			snap.Message = "portal online, Internet unreachable"
		}

		if prev.State != StateOnline {
			switch {
			case !down.IsZero():
				logging.Log.Info("session is back online after %s offline (account %s, client ip %s)",
					shortDuration(time.Since(down)), st.Account, st.ClientIP)
			case !lastOnline.IsZero():
				logging.Log.Info("session is online again, last read online %s ago"+
					" (account %s, client ip %s)",
					shortDuration(time.Since(lastOnline)), st.Account,
					st.ClientIP)
			default:
				logging.Log.Info("session is online (account %s, client ip %s)", st.Account, st.ClientIP)
			}
		}
		if internetFailed {
			// Only on the transition: an unreachable probe host would otherwise
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
		// An entry with no password and a file with no accounts are different
		// faults with different fixes, and the reader only sees this line.
		if cfg.HasAccounts() {
			snap.Message = "the account has no password, open the settings window"
		} else {
			snap.Message = "no account configured, open the settings window"
		}
		snap.LastError = snap.Message
		// Compared by message too: the round before may already have been an
		// Error for another reason, which would hide this one.
		if prev.State != StateError || prev.Message != snap.Message {
			logging.Log.Warn("%s%s", snap.Message, offline)
		}

	default:
		snap = s.signIn(cfg, client, st, snap, offline)
	}

	// downSince is what a recovery is measured from, so it is set on the rounds
	// the session was read as not signed in and cleared on the round it was read
	// as signed in. A round that could not read the answer leaves it alone: the
	// session may never have dropped, and the log used to say it had.
	s.mu.Lock()
	switch {
	case !st.Reachable || !st.Known:
	case st.Online:
		s.downSince = time.Time{}
	default:
		if s.downSince.IsZero() {
			s.downSince = time.Now()
		}
	}
	if s.backoffWait <= 0 {
		// Nothing in this round was the portal's own trouble, so the run of
		// failures is over and the next check keeps the ordinary interval.
		s.transientStreak = 0
	}
	s.mu.Unlock()

	// The entries being left alone go into every snapshot, whatever this round
	// ended up doing, so the page can mark them.
	snap.Skipped = s.skipped(time.Now())

	logging.Log.Debug("check #%d finished in %d ms: %s", checks, elapsedMS(start), DescribeSnapshot(snap))
	s.publish(snap)

	// After the session has been published, so a release page that answers slowly
	// holds up the next look at the portal and never the one that just happened.
	s.checkForUpdate(cfg, time.Now())
}

// updateInterval is how long between two looks for a newer release. The check
// loop runs every fifteen seconds and a release is published rarely, so the two
// are hours apart; a look that failed waits the same, being best effort. It is a
// variable so a test does not have to spend the wait.
var updateInterval = 6 * time.Hour

// checkForUpdate looks for a newer release when the reader asked for it and
// enough time has passed, and installs what it found when this copy can take it.
//
// It shares nothing with the session but the snapshot: a release page that
// cannot be reached is a line in the log, and nothing here signs in or reports
// the session as broken.
func (s *Service) checkForUpdate(cfg *config.Config, now time.Time) {
	// The switch is the reader's, and it is off until they turn it on: with it
	// off the forge is not asked for a check and not asked for an install.
	if !cfg.CheckForUpdates {
		return
	}
	s.mu.Lock()
	up, due := s.updates, !now.Before(s.nextUpdateAt)
	ctx := s.updateCtx
	if due {
		// Recorded before the request, so a forge that cannot be reached is not
		// asked again on the next check either.
		s.nextUpdateAt = now.Add(updateInterval)
	}
	s.mu.Unlock()
	if up == nil || !due {
		return
	}

	// Off the loop. A release is a few megabytes, and the loop's next job is to
	// ask the portal whether the session is still up.
	s.updateWG.Add(1)
	go func() {
		defer s.updateWG.Done()
		s.lookForUpdate(ctx, cfg, up)
	}()
}

// lookForUpdate is the look itself, on a goroutine of its own. The context is the
// one the run made for it and is cancelled when the loop is stopped, so a download
// in flight does not decide how long the program takes to close.
func (s *Service) lookForUpdate(ctx context.Context, cfg *config.Config, up Updater) {
	channel := cfg.UpdateChannelValue()
	logging.Log.Debug("check: looking for a newer release on the %s channel", channel)
	check, err := up.Check(ctx, channel)
	if err != nil {
		logging.Log.Warn("could not ask the release page: %v", err)
		return
	}

	out := updateFrom(check)
	// Published before anything is downloaded, so the page and the tray show the
	// release while an install is still running.
	s.noteUpdate(out)

	switch {
	case !check.Newer:
		logging.Log.Info("%s", check.Describe())
	case !check.Signed:
		// Nothing is downloaded while the release cannot be checked: what the
		// server handed over would be what runs.
		logging.Log.Warn("%s", check.Describe())
	case !check.Install.Possible:
		// The ordinary answer for a copy a package manager owns, and not a fault.
		logging.Log.Info("%s", check.Describe())
	default:
		path, err := up.Install(ctx, check.Release)
		if err != nil {
			logging.Log.Warn("could not install %s: %v", check.Latest, err)
			return
		}
		out.Installed = path
		out.Message = check.Latest + " was installed over " + path +
			", it takes effect on the next start"
		logging.Log.Info("%s", out.Message)
		s.noteUpdate(out)
	}
}

// updateFrom is what a look at the forge found, as the snapshot carries it.
func updateFrom(c update.Check) *Update {
	out := &Update{
		CheckedAt:   time.Now().UTC().Format(time.RFC3339),
		Message:     c.Describe(),
		Current:     c.Current,
		Latest:      c.Latest,
		Channel:     string(c.Channel),
		Newer:       c.Newer,
		Signed:      c.Signed,
		Installable: c.Install.Possible,
	}
	if !c.Install.Possible {
		out.Reason = c.Install.Reason
	}
	return out
}

// CheckForUpdateNow looks for a newer release at once, and answers whether there
// was anything to look with.
//
// This is the reader asking, so it runs whether or not the switch is on: the
// switch covers the automatic look, the same way the update command ignores it.
// The interval is skipped for the same reason. The look is on its own goroutine,
// as every look is, so the caller is not held while a release is downloaded.
func (s *Service) CheckForUpdateNow() bool {
	s.mu.Lock()
	up, cfg := s.updates, s.cfg
	if s.updateCtx == nil || s.updateCtx.Err() != nil {
		// The run that is over cancelled the context it used on its way out, and
		// the reader is asking anyway: this look needs one nothing has cancelled.
		s.updateCtx, s.cancelUpdates = context.WithCancel(context.Background())
	}
	ctx := s.updateCtx
	s.mu.Unlock()
	if up == nil || cfg == nil {
		return false
	}
	s.updateWG.Add(1)
	go func() {
		defer s.updateWG.Done()
		s.lookForUpdate(ctx, cfg, up)
	}()
	return true
}

// noteUpdate keeps what a look found and wakes the loop to publish it.
//
// The loop is the only publisher. A snapshot written from the look itself could
// be older than the one a check is about to publish, and the interface would go
// back a step: the session shown would be the one from before that check.
func (s *Service) noteUpdate(u *Update) {
	s.mu.Lock()
	s.update = u
	s.mu.Unlock()
	select {
	case s.triggerCh <- struct{}{}:
	default:
	}
}

// signIn walks the login list and submits the first attempt that is not being
// skipped. A refusal that blames the account moves on; a busy portal, a timeout
// or an unreadable reply ends the round without marking every account broken.
//
// Two accounts of one round are never submitted back to back: see loginSpacing.
func (s *Service) signIn(cfg *config.Config, client *portal.Client, st portal.Status, snap Snapshot, offline string) Snapshot {
	now := time.Now()
	base := time.Duration(cfg.CheckIntervalSeconds) * time.Second
	s.pruneRejected(now)

	// This is a session signed in from scratch, so a move asked for on the one
	// before does not apply to it.
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
		if i > 0 {
			// A portal that is collecting passwords answers every entry with a
			// refusal the account cannot fix, and this used to hand it the whole
			// list one round trip apart. The wait is what keeps a round from being
			// a burst; it is not what makes the list safe to send.
			s.wait(spacingBeforeNextLogin(rand.Float64()))
		}
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
		res := client.Login(st, a)
		if !res.Success {
			s.mu.Lock()
			s.failStreak++
			s.mu.Unlock()

			if transient(res) {
				// The portal could not answer about an account, which is the
				// portal's own trouble: the check after this one waits longer for
				// it.
				streak, wait := s.noteTransientFailure(base)
				snap.State = StateError
				snap.LastError = res.Message
				snap.Message = "login failed: " + res.Message
				logging.Log.Warn("reconnect attempt #%d as %s rejected after %d ms: %s (ret_code=%s, raw %s)%s",
					attempt, a.Entry(), elapsedMS(attemptStart), res.Message, res.RetCode, portal.Truncate(res.Raw), offline)
				// A code outside the table is named as unknown, not called the portal's: an
				// entry that keeps drawing such an answer is never passed over.
				if text, known := portal.RetCodeText(res.RetCode); known {
					logging.Log.Warn("the portal answered ret_code=%s (%s), taken for the portal's own trouble, so the rest of the list waits for the next check",
						res.RetCode, text)
				} else {
					logging.Log.Warn("the portal answered ret_code=%s, which is not one of the codes this program knows. It is taken for the portal's own trouble, so the rest of the list waits for the next check, and an entry that keeps drawing this answer is the one to look at",
						res.RetCode)
				}
				logReconnectFailure(attempt, streak, logSeconds(wait), offline)
				return snap
			}

			// Refused for a reason waiting will not fix, so it is left alone for
			// a while and the next entry is tried.
			s.reject(a, res, now)
			logging.Log.Warn("reconnect attempt #%d as %s was refused: %s (ret_code=%s, raw %s)%s",
				attempt, a.Entry(), res.Message, res.RetCode, portal.Truncate(res.Raw), offline)
			if i+1 < len(ready) {
				logging.Log.Warn("entry %d is left alone for %s, moving on to the next account",
					a.Index+1, shortDuration(cooldownPeriod))
			} else {
				// Nothing is left to move on to, and saying it does would be the
				// one line a single-account installation ever sees.
				logging.Log.Warn("entry %d is left alone for %s, and there is no entry after it",
					a.Index+1, shortDuration(cooldownPeriod))
			}

			snap.State = StateError
			snap.LastError = res.Message
			snap.Message = fmt.Sprintf("%s was refused: %s", a.Entry(), res.Message)
			continue
		}

		// The login endpoint can report success while the portal still shows the
		// client as offline, so confirm before declaring victory.
		verify := s.confirmLogin(client)
		if !verify.Online {
			// Neither reading proves the login failed: the two endpoints are written a
			// moment apart, so a list that has not caught up reads the same as a login
			// that did not take. The state stays "signing in" and the next check settles
			// it.
			snap.State = StateLoggingIn
			snap.Message = "signed in as " + a.Entry() + ", not confirmed yet"
			if verify.Known {
				logging.Log.Warn("reconnect attempt #%d as %s: the login was accepted and the portal's online list still shows the client as offline after %d reads over %s (%s), the next check settles it, raw login reply %s",
					attempt, a.Entry(), confirmTries,
					shortDuration(time.Duration(confirmTries-1)*confirmDelay), verify.Message, portal.Truncate(res.Raw))
			} else {
				logging.Log.Warn("reconnect attempt #%d as %s: the login was accepted and the session could not be read back within %s (%s), the next check settles it, raw login reply %s",
					attempt, a.Entry(), shortDuration(time.Duration(confirmTries-1)*confirmDelay),
					verify.Message, portal.Truncate(res.Raw))
			}
			return snap
		}

		logging.Log.Info("reconnect attempt #%d succeeded in %d ms as %s (entry %d of %d), account %s, client ip %s, session since %s",
			attempt, elapsedMS(attemptStart), a.Entry(), a.Index+1, len(cfg.Accounts),
			verify.Account, verify.ClientIP, verify.OnlineSince)
		return s.signedIn(snap, a, verify, "signed in as")
	}

	return s.allRefused(snap, now, offline)
}

// confirmLogin reads the session back after a login the portal accepted and
// hands back what the portal said. See confirmTries.
func (s *Service) confirmLogin(client *portal.Client) portal.Status {
	verify := client.QueryStatus()
	for i := 1; i < confirmTries && !verify.Online; i++ {
		time.Sleep(confirmDelay)
		verify = client.QueryStatus()
	}
	return verify
}

// signedIn is what a login the portal confirmed leaves behind: the snapshot the
// interface is shown and the session facts every later snapshot is built from.
// verb is how the log and the page word it: "signed in as" or "switched to".
func (s *Service) signedIn(snap Snapshot, a config.Attempt, verify portal.Status, verb string) Snapshot {
	last := time.Now().Format("01-02 15:04:05")
	entry := a.Entry()
	s.mu.Lock()
	s.lastLogin = last
	s.failStreak = 0
	s.lastOnlineAt = time.Now()
	s.activeIndex, s.activeEntry = a.Index, entry
	// A new session replaces the one a refusal to move was about. Nothing of that
	// refusal carries over.
	s.switchRefused = nil
	s.switchAskedAt = time.Time{}
	s.mu.Unlock()

	snap.State = StateOnline
	snap.LastLoginAt = last
	snap.Account = verify.Account
	snap.ClientIP = verify.ClientIP
	snap.OnlineSince = verify.OnlineSince
	snap.Entry, snap.EntryIndex = entry, a.Index
	// The portal echoes the account it was given, so the type the login went out
	// with is the type the session is on. The switch has nothing left to do.
	snap.UsedISP = a.ISP
	snap.MoveToPreferred = false
	snap.Message = verb + " " + entry + " at " + last
	return snap
}

// attribute records which entry of the list the portal says the session is on,
// so the interface can mark it, a session this program did not sign in itself
// included. It also reports the service type that entry puts first.
func (s *Service) attribute(cfg *config.Config, st portal.Status, snap Snapshot) Snapshot {
	if st.Account == "" {
		// Without an account name from the portal there is no entry to match, so
		// nothing is marked and no move is offered.
		logging.Log.Debug("the portal reports a session with no account name on it, so it cannot be matched to an entry of the login list")
		return snap
	}
	if index, isp := cfg.Match(st.Account); index >= 0 {
		label := cfg.EntryLabel(index, isp)
		s.setActive(index, label)
		snap.UsedISP = isp
		logging.Log.Debug("the session is on %s: entry %d, service type %s", label, index+1, config.OperatorName(isp))
	} else {
		logging.Log.Debug("the session's account %q names no entry of the login list, so no entry is marked and no service type is offered",
			st.Account)
		s.setActive(-1, "")
		snap.UsedISP = ""
	}
	snap.Entry, snap.EntryIndex = s.active()

	snap.PreferredISP = ""
	snap.MoveToPreferred = false
	a, ok := cfg.PreferredFor(st.Account)
	if !ok {
		// The session is where its entry puts first. The refusal to move it and
		// the wait are dropped here, so neither is reported if the session moves
		// elsewhere later.
		s.mu.Lock()
		hadRefused := s.switchRefused != nil
		s.switchRefused = nil
		s.switchAskedAt = time.Time{}
		s.mu.Unlock()
		if hadRefused {
			logging.Log.Info("the session is on the service type its entry puts first, forgetting the earlier refusal to move it")
		}
		return snap
	}
	snap.PreferredISP = a.ISP
	snap.MoveToPreferred = true
	s.mu.Lock()
	refused := s.switchRefused
	s.mu.Unlock()
	if refused != nil {
		// Read back every round the session is up, so the page's warning and the
		// tray's tooltip stay in agreement.
		logging.Log.Debug("the session is still on %s and has not been moved onto %s: the portal refused it (%s, ret_code=%s), the settings page and the tray say so",
			config.OperatorName(snap.UsedISP), config.OperatorName(a.ISP), refused.Message, refused.RetCode)
	}
	snap.SwitchRefused = refused
	return snap
}

// preferredPending reports the attempt a session that is already up should be
// moved onto, and false when there is nothing to do. Moves stay inside one
// entry: another entry's account would replace the working session.
func (s *Service) preferredPending(cfg *config.Config, st portal.Status) (config.Attempt, bool) {
	if !cfg.SwitchToPreferredService {
		logging.Log.Debug("not moving the session off %s: the option that moves a session onto the first service type of its entry is off",
			st.Account)
		return config.Attempt{}, false
	}
	if !cfg.AutoLogin {
		// Moving a session is a sign-in like any other, and one switch already
		// answers whether this program signs in on its own. A reader who turned
		// that off is left to the button on the page.
		logging.Log.Debug("not moving the session off %s: automatic login is off, so only the button on the settings page asks for it",
			st.Account)
		return config.Attempt{}, false
	}
	s.mu.Lock()
	askedAt := s.switchAskedAt
	s.mu.Unlock()
	if wait := switchRetryPeriod - time.Since(askedAt); !askedAt.IsZero() && wait > 0 {
		logging.Log.Debug("not moving the session off %s: it was asked about %s ago and refused, asking again in %s",
			st.Account, shortDuration(time.Since(askedAt)), shortDuration(wait))
		return config.Attempt{}, false
	}
	a, ok := cfg.PreferredFor(st.Account)
	if !ok {
		// Two things look alike: the session is already on the type its entry
		// puts first, or its account names no entry. The entry label tells them
		// apart, being empty for the second.
		if stays := s.entryLabel(cfg, st); stays != "" {
			logging.Log.Debug("not moving the session off %s: that is the service type its entry puts first", stays)
		} else {
			logging.Log.Debug("not moving the session reported as %q: it names no entry of the login list", st.Account)
		}
		return config.Attempt{}, false
	}
	if s.cooling(a) {
		// The session stays where it is. Moving onto that service type was refused
		// and is still waiting out its cooldown.
		logging.Log.Debug("not moving the session off %s onto %s: that attempt is still being left alone after the portal refused it",
			st.Account, a.Entry())
		return config.Attempt{}, false
	}
	if !askedAt.IsZero() {
		logging.Log.Debug("the session on %s has waited out the %s after the last refusal, asking the portal to move it onto %s",
			st.Account, shortDuration(switchRetryPeriod), a.Entry())
	}
	return a, true
}

// switchToPreferred signs in again while the session is up, to move it onto the
// service type its entry puts first. The portal keeps one session per client
// address, so the sign-in replaces the session or is refused.
func (s *Service) switchToPreferred(cfg *config.Config, client *portal.Client, st portal.Status, snap Snapshot, a config.Attempt) Snapshot {
	// The hour-long wait is set once the answer is in, further down: setting it
	// before the request made a dial timeout look like a refusal and left the
	// session on the service type the reader had not asked for.
	s.mu.Lock()
	s.attempts++
	attempt := s.attempts
	s.mu.Unlock()

	logging.Log.Info("the session is on %s, which is not the service type its entry puts first, signing in again as %s (attempt #%d)",
		st.Account, a.Entry(), attempt)

	start := time.Now()
	res := client.Login(st, a)
	if !res.Success {
		if transient(res) {
			// Nothing was answered about the account, so there is nothing to
			// wait out and the next check asks again.
			logging.Log.Warn("could not ask the portal to move the session onto %s: %s (ret_code=%s, raw %s); asking again at the next check",
				a.Entry(), res.Message, res.RetCode, portal.Truncate(res.Raw))
			return s.attribute(cfg, st, s.stillOnline(snap, st,
				"the portal could not be asked to move the session onto "+a.Entry()+", trying again"))
		}

		s.mu.Lock()
		s.switchAskedAt = time.Now()
		s.mu.Unlock()

		// The portal's answer is kept for the page as well as logged. The session
		// stays as it is either way.
		s.setSwitchRefused(&Refusal{Message: res.Message, RetCode: res.RetCode})
		logging.Log.Warn("the portal would not sign in again while the session is up: %s (ret_code=%s, raw %s); the session is left as it is and asked about again in %s",
			res.Message, res.RetCode, portal.Truncate(res.Raw), shortDuration(switchRetryPeriod))
		message := "the portal would not sign in again while the session is up"
		if stays := s.entryLabel(cfg, st); stays != "" {
			message += ", the session stays as " + stays
		}
		return s.attribute(cfg, st, s.stillOnline(snap, st, message))
	}

	verify := s.confirmLogin(client)
	if !verify.Online {
		// The sign-in was accepted, so nothing was refused and there is nothing
		// to wait out: the next check reads which service type the session ended
		// up on.
		logging.Log.Warn("the portal accepted the second sign-in as %s and does not list the session as online (%s), raw login reply %s; the next check reads the session state again",
			a.Entry(), verify.Message, portal.Truncate(res.Raw))
		return s.attribute(cfg, st, s.stillOnline(snap, st,
			"signed in again as "+a.Entry()+", not confirmed yet"))
	}

	logging.Log.Info("signed in again in %d ms, the session is now %s, since %s",
		elapsedMS(start), a.Entry(), verify.OnlineSince)
	return s.signedIn(snap, a, verify, "switched to")
}

// setSwitchRefused remembers what the portal answered when it would not sign in
// again to move the session. The page and the tray report it.
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
// move it. It is a working session, so it is reported online with the reason in
// the message.
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
// or still waiting out a refusal. The refusals were logged when they happened;
// this says what the next try is waiting for.
func (s *Service) allRefused(snap Snapshot, now time.Time, offline string) Snapshot {
	snap.State = StateError
	skipped := s.skipped(now)
	wait := shortDuration(s.shortestWait(now))
	if len(skipped) == 1 {
		// One entry is the common case, and its reason says more than the news
		// that the list was walked.
		snap.Message = fmt.Sprintf("%s, trying again in %s", skipped[0].Message, wait)
	} else {
		snap.Message = fmt.Sprintf("every account was refused by the portal, trying again in %s", wait)
	}
	snap.LastError = snap.Message
	logging.Log.Debug("%s%s", snap.Message, offline)
	return snap
}

// transient reports whether a rejected login says something about the portal.
// Only the codes that name the account itself give up on an entry; anything
// else, including a code this program has never seen, waits for the next check.
func transient(res portal.LoginResult) bool {
	switch res.RetCode {
	case "1", // wrong account or password
		"7": // authentication failed
		return false
	}
	// Every code outside the two counts as the portal's own trouble, so the entry
	// keeps its place: during a fault of the portal's own, walking the list would
	// submit a second login to a portal that is already struggling. README.md says
	// what that costs.
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

// pruneRejectedLocked is pruneRejected with the lock already held. It appends
// what it drops to dst, so a caller with a buffer to spare keeps this free of
// allocation while s.mu is held; a nil dst allocates only when a wait ends.
func (s *Service) pruneRejectedLocked(dst []rejection, now time.Time) []rejection {
	for key, r := range s.rejected {
		if !now.Before(r.until) {
			delete(s.rejected, key)
			dst = append(dst, r)
		}
	}
	return dst
}

// logExpired reports the refusals whose wait has ended, called after the lock
// is released. Both paths that prune have to log it, signIn for a round that is
// offline and skipped for one that is online.
func logExpired(expired []rejection) {
	for _, r := range expired {
		logging.Log.Info("entry %d is out of its %s cooldown and is tried again (it had been refused: %s, ret_code=%s)",
			r.index+1, shortDuration(cooldownPeriod), r.message, r.retCode)
	}
}

// skipped renders the entries that are being left alone, for the interface, and
// prunes first: an entry a round that never reached signIn let expire must not
// be reported as still being left alone. The locked section allocates nothing.
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
			// The entry is still held back, so "0s" would read as "due now" and
			// a negative value as nonsense. Anything below a second is rounded
			// up to it.
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

	// slices.SortFunc allocates nothing here; sort.Slice goes through
	// reflect.Swapper on every call.
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
	// Left out when the message already names it, as the line written right
	// after a successful sign-in does.
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
// summary line is raised to ERROR, so a sustained outage stands out from one
// transient failure.
const failStreakErrorLevel = 3

// offlineSuffix renders ", offline for 4m10s" once a previous online moment is
// known, which keeps the outage duration visible during a long failure run.
// notKnownSuffix is how long the session state has not been readable, which is
// not how long the session has been down. Only a round that read the answer can
// say anything about the session, and these are the rounds that did not.
func notKnownSuffix(lastKnown time.Time) string {
	if lastKnown.IsZero() {
		return ""
	}
	return ", the state has not been readable for " +
		shortDuration(time.Since(lastKnown))
}

func offlineSuffix(lastOnline time.Time) string {
	if lastOnline.IsZero() {
		return ""
	}
	return ", offline for " + shortDuration(time.Since(lastOnline))
}

func logReconnectFailure(attempt int64, streak, retrySeconds int, offline string) {
	if streak >= failStreakErrorLevel {
		logging.Log.Error("reconnect attempt #%d failed, %d consecutive failures%s, next try in %ds",
			attempt, streak, offline, retrySeconds)
		return
	}
	logging.Log.Warn("reconnect attempt #%d failed, %d consecutive failure(s)%s, next try in %ds",
		attempt, streak, offline, retrySeconds)
}

// noteTransientFailure records a round that ended on the portal's own trouble and
// schedules the check after it: how long from now, and how many such rounds this
// is in a row.
func (s *Service) noteTransientFailure(base time.Duration) (streak int, wait time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transientStreak++
	s.backoffWait = nextBackoff(base, s.transientStreak, rand.Float64())
	return s.transientStreak, s.backoffWait
}

// waitBeforeNextCheck is how long the loop waits before it checks again: the
// configured interval, or the longer wait a run of failures earned in the round
// that just ended.
func (s *Service) waitBeforeNextCheck(base time.Duration) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backoffWait <= 0 {
		return base
	}
	return s.backoffWait
}

// nextBackoff is how long to wait before the check that follows failures rounds
// in a row that went wrong on the portal's side. The first one waits the ordinary
// interval, so a single failed round is retried exactly as it was before there was
// any backoff; every failure after it doubles the wait, up to maxBackoff, and up
// to backoffJitterFraction of it is added at random so that clients of one campus
// do not all come back at the same moment. rnd is a value in [0,1).
func nextBackoff(base time.Duration, failures int, rnd float64) time.Duration {
	if failures <= 0 || base <= 0 {
		return base
	}
	wait := base
	for i := 1; i < failures && wait < maxBackoff; i++ {
		wait *= 2
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}
	switch {
	case rnd <= 0:
		return wait
	case rnd > 1:
		rnd = 1
	}
	return wait + time.Duration(float64(wait)*backoffJitterFraction*rnd)
}

// spacingBeforeNextLogin is how long a round waits before submitting the next
// account of the login list: a floor plus a part at random, so that the wait is
// never the same twice. rnd is a value in [0,1).
func spacingBeforeNextLogin(rnd float64) time.Duration {
	switch {
	case rnd < 0:
		rnd = 0
	case rnd > 1:
		rnd = 1
	}
	return loginSpacing + time.Duration(float64(loginSpacingJitter)*rnd)
}

// wait pauses the round. The pause is the one the service was built with, so a
// test has the waits reported to it instead of spending them. The field is read
// under the lock, which lets a test set it on a service that is already running.
func (s *Service) wait(d time.Duration) {
	if d <= 0 {
		return
	}
	s.mu.Lock()
	sleep := s.sleep
	s.mu.Unlock()
	if sleep != nil {
		sleep(d)
	}
}

// logBackoff says that the loop is waiting longer than its interval because rounds
// in a row went wrong on the portal's side. Said as a warning while the wait is
// first growing and only in the debug log after that, so a long outage does not
// fill the log with the same line.
func logBackoff(what string, streak int, wait, base time.Duration) {
	if wait <= base {
		return
	}
	line := fmt.Sprintf("%s %d checks in a row, the next check is in %s instead of %s",
		what, streak, shortDuration(wait), shortDuration(base))
	if streak >= 2 {
		logging.Log.Warn("%s", line)
		return
	}
	logging.Log.Debug("%s", line)
}

// logSeconds is a wait in whole seconds, for the log line that says when the next
// try is.
func logSeconds(d time.Duration) int {
	return int(d.Round(time.Second) / time.Second)
}

// waitClosed waits for ch to be closed, for at most limit, and reports whether it
// was.
func waitClosed(ch <-chan struct{}, limit time.Duration) bool {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}

// waitGroup is WaitGroup.Wait with a bound. The goroutine it starts ends as soon
// as the group does, so giving up on the wait leaves nothing of its own behind.
func waitGroup(wg *sync.WaitGroup, limit time.Duration) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	return waitClosed(done, limit)
}
