package main

import (
	"sync"
	"time"
)

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
}

// Service polls the portal and signs back in when the session is gone.
type Service struct {
	mu        sync.Mutex
	cfg       *Config
	client    *Client
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
}

func NewService(cfg *Config) *Service {
	return &Service{
		cfg:       cfg,
		client:    NewClient(cfg),
		triggerCh: make(chan struct{}, 1),
		snap:      Snapshot{State: StateUnknown, Message: "waiting for first check"},
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
	Log.Info("service started: %s", s.cfg.Summary())
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
	Log.Debug("service stopped (checks=%d, login attempts=%d)", checks, attempts)
}

func (s *Service) stats() (checks, attempts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checks, s.attempts
}

// Restart applies a new configuration without recreating the Service.
func (s *Service) Restart(cfg *Config) {
	s.Stop()
	s.mu.Lock()
	s.cfg = cfg
	s.client = NewClient(cfg)
	s.mu.Unlock()
	s.Start()
}

// Trigger asks for an immediate check; it never blocks.
func (s *Service) Trigger() {
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
			Log.Debug("check requested out of schedule")
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

	Log.Debug("check #%d: querying portal, previous state %s", checks, prev.State)

	st := s.client.QueryStatus()
	Log.Debug("check #%d: reachable=%v online=%v clientIp=%q detail=%q",
		checks, st.Reachable, st.Online, st.ClientIP, st.Message)

	snap := Snapshot{ClientIP: st.ClientIP, Account: st.Account, OnlineSince: st.OnlineSince}
	s.mu.Lock()
	snap.LastLoginAt = s.lastLogin
	s.mu.Unlock()

	offline := offlineSuffix(lastOnline)

	switch {
	case !st.Reachable:
		snap.State = StateError
		snap.Message = st.Message
		snap.LastError = st.Message
		if prev.State != StateError || prev.Message != st.Message {
			Log.Warn("portal unreachable: %s", st.Message)
		} else {
			Log.Debug("portal still unreachable: %s", st.Message)
		}

	case !st.Known:
		// The portal answered but the session state could not be read. Reading
		// that as a dropped session would fire a pointless login on every
		// transient failure, so skip the reconnect for this round.
		snap.State = StateError
		snap.Message = "session state unknown: " + st.Message
		snap.LastError = snap.Message
		if prev.State != StateError || prev.Message != snap.Message {
			Log.Warn("cannot read the session state: %s, skipping the reconnect this round%s",
				st.Message, offline)
		} else {
			Log.Debug("session state still unknown: %s", st.Message)
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
				Log.Info("session is back online after %s offline (account %s, client ip %s)",
					shortDuration(time.Since(lastOnline)), st.Account, st.ClientIP)
			} else {
				Log.Info("session is online (account %s, client ip %s)", st.Account, st.ClientIP)
			}
		}
		if internetFailed {
			// Only on the transition, otherwise an unreachable probe host would
			// warn once per check.
			if prev.State != StateOnline || prev.InternetOK {
				Log.Warn("portal reports online but no probe host answered without interception")
			} else {
				Log.Debug("probe hosts still unreachable while the portal reports online")
			}
		}

		s.mu.Lock()
		s.lastOnlineAt = time.Now()
		s.failStreak = 0
		s.mu.Unlock()

	case !cfg.AutoLogin:
		snap.State = StateOffline
		snap.Message = "auto login is disabled"
		if prev.State != StateOffline {
			Log.Warn("session is down but auto login is disabled, staying offline%s", offline)
		}

	case !cfg.Configured():
		snap.State = StateError
		snap.Message = "no account configured, open the settings window"
		snap.LastError = snap.Message
		// Compared by message as well: the previous round may already have been
		// an Error for a different reason, which would hide this one.
		if prev.State != StateError || prev.Message != snap.Message {
			Log.Warn("%s%s", snap.Message, offline)
		}

	default:
		snap.State = StateLoggingIn
		snap.Message = "disconnected, signing in..."
		s.publish(snap)

		s.mu.Lock()
		s.attempts++
		attempt := s.attempts
		s.mu.Unlock()
		Log.Info("session is down, reconnect attempt #%d for account %q (client ip %q)%s",
			attempt, cfg.Username, st.ClientIP, offline)

		attemptStart := time.Now()
		res := s.client.Login(st)
		if !res.Success {
			s.mu.Lock()
			s.failStreak++
			streak := s.failStreak
			s.mu.Unlock()

			snap.State = StateError
			snap.LastError = res.Message
			snap.Message = "login failed: " + res.Message
			Log.Warn("reconnect attempt #%d rejected after %d ms: %s (ret_code=%s, raw %s)%s",
				attempt, elapsedMS(attemptStart), res.Message, res.RetCode, truncate(res.Raw), offline)
			logReconnectFailure(attempt, streak, cfg.CheckIntervalSeconds, offline)
			break
		}

		// The login endpoint can report success while the portal still shows
		// the client as offline, so confirm before declaring victory.
		verify := s.client.QueryStatus()
		if verify.Online {
			last := time.Now().Format("01-02 15:04:05")
			s.mu.Lock()
			s.lastLogin = last
			s.failStreak = 0
			s.lastOnlineAt = time.Now()
			s.mu.Unlock()

			snap.State = StateOnline
			snap.LastLoginAt = last
			snap.Account = verify.Account
			snap.ClientIP = verify.ClientIP
			snap.OnlineSince = verify.OnlineSince
			snap.Message = "signed in at " + last
			Log.Info("reconnect attempt #%d succeeded in %d ms, account %s, client ip %s, session since %s",
				attempt, elapsedMS(attemptStart), verify.Account, verify.ClientIP, verify.OnlineSince)
		} else {
			s.mu.Lock()
			s.failStreak++
			streak := s.failStreak
			s.mu.Unlock()

			snap.State = StateError
			snap.Message = "portal accepted the login but still reports offline"
			snap.LastError = snap.Message
			Log.Warn("reconnect attempt #%d: the login was accepted but the follow-up query says offline (portal reachable=%v, detail %q), raw login reply %s",
				attempt, verify.Reachable, verify.Message, truncate(res.Raw))
			logReconnectFailure(attempt, streak, cfg.CheckIntervalSeconds, offline)
		}
	}

	Log.Debug("check #%d finished in %d ms: %s", checks, elapsedMS(start), describeSnapshot(snap))
	s.publish(snap)
}

func (s *Service) publish(snap Snapshot) {
	s.mu.Lock()
	s.snap = snap
	f := s.listener
	s.mu.Unlock()
	if f != nil {
		Log.Debug("publishing status to ui: %s", describeSnapshot(snap))
		f(snap)
	}
}

func describeSnapshot(s Snapshot) string {
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
	return out
}

func shortDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
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
		Log.Error("reconnect attempt #%d failed, %d consecutive failures%s, next try in %ds",
			attempt, streak, offline, retrySeconds)
		return
	}
	Log.Warn("reconnect attempt #%d failed, %d consecutive failure(s)%s, next try in %ds",
		attempt, streak, offline, retrySeconds)
}
