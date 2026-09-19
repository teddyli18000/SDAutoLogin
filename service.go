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
	Log.Info("service started, polling every %ds", s.cfg.CheckIntervalSeconds)
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
			s.tick()
			timer.Reset(interval)
		}
	}
}

func (s *Service) tick() {
	cfg := s.cfg
	st := s.client.QueryStatus()

	snap := Snapshot{ClientIP: st.ClientIP, Account: st.Account, OnlineSince: st.OnlineSince}
	s.mu.Lock()
	snap.LastLoginAt = s.lastLogin
	s.mu.Unlock()

	switch {
	case !st.Reachable:
		snap.State = StateError
		snap.Message = st.Message
		snap.LastError = st.Message
		s.logChange(snap, "portal unreachable: %s", st.Message)

	case st.Online:
		if cfg.CheckInternet {
			snap.InternetChecked = true
			snap.InternetOK = s.client.ProbeInternet()
		} else {
			snap.InternetOK = true
		}
		snap.State = StateOnline
		if snap.InternetChecked && !snap.InternetOK {
			snap.Message = "portal online, Internet unreachable"
		}
		s.logChange(snap, "session is up (account %s, client ip %s)", st.Account, st.ClientIP)

	case !cfg.AutoLogin:
		snap.State = StateOffline
		snap.Message = "auto login is disabled"

	case !cfg.Configured():
		snap.State = StateError
		snap.Message = "no account configured, open the settings window"
		snap.LastError = snap.Message
		s.logChange(snap, "%s", snap.Message)

	default:
		snap.State = StateLoggingIn
		snap.Message = "disconnected, signing in..."
		s.publish(snap)
		if st.ClientIP == "" {
			Log.Warn("no client ip parsed from the portal page, login may fail")
		}
		Log.Info("session is down, submitting login (client ip %q)", st.ClientIP)

		res := s.client.Login(st)
		if !res.Success {
			snap.State = StateError
			snap.LastError = res.Message
			snap.Message = "login failed: " + res.Message
			Log.Warn("login rejected: %s (ret_code=%s, raw %s)", res.Message, res.RetCode, truncate(res.Raw))
			break
		}

		// The login endpoint can report success while the portal still shows
		// the client as offline, so confirm before declaring victory.
		verify := s.client.QueryStatus()
		if verify.Online {
			last := time.Now().Format("01-02 15:04:05")
			s.mu.Lock()
			s.lastLogin = last
			s.mu.Unlock()
			snap.State = StateOnline
			snap.LastLoginAt = last
			snap.Account = verify.Account
			snap.ClientIP = verify.ClientIP
			snap.OnlineSince = verify.OnlineSince
			snap.Message = "signed in at " + last
			Log.Info("login succeeded, account %s, client ip %s", cfg.Username, verify.ClientIP)
		} else {
			snap.State = StateError
			snap.Message = "portal accepted the login but still reports offline"
			snap.LastError = snap.Message
			Log.Warn("%s (raw %s)", snap.Message, truncate(res.Raw))
		}
	}

	s.publish(snap)
}

// logChange logs only when the rendered status actually changes.
func (s *Service) logChange(next Snapshot, format string, args ...any) {
	s.mu.Lock()
	changed := s.snap.State != next.State || s.snap.Message != next.Message
	s.mu.Unlock()
	if changed {
		Log.Info(format, args...)
	}
}

func (s *Service) publish(snap Snapshot) {
	s.mu.Lock()
	s.snap = snap
	f := s.listener
	s.mu.Unlock()
	if f != nil {
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
