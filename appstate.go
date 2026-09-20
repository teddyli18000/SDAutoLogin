package main

import "sync"

// appState owns the mutable configuration and the running service, so the
// tray, the settings service and the command line all read and write the same
// values instead of keeping private copies.
type appState struct {
	mu      sync.Mutex
	cfg     *Config
	svc     *Service
	started bool
}

func newAppState(cfg *Config) *appState {
	return &appState{cfg: cfg, svc: NewService(cfg)}
}

func (a *appState) Config() *Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// Snapshot returns the latest status shown to the user.
func (a *appState) Snapshot() Snapshot {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	return svc.Snapshot()
}

// Start begins the periodic check, once.
func (a *appState) Start() {
	a.mu.Lock()
	svc, first := a.svc, !a.started
	a.started = true
	a.mu.Unlock()
	if first {
		svc.Start()
	}
}

func (a *appState) Stop() {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	svc.Stop()
}

func (a *appState) Trigger() {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	svc.Trigger()
}

func (a *appState) SetListener(f func(Snapshot)) {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	svc.SetListener(f)
}

// Reload re-reads config.json, applies it and returns the new configuration.
func (a *appState) Reload() (*Config, error) {
	next, err := LoadConfig(a.Config().Path())
	if err != nil {
		return nil, err
	}
	if err := a.Update(next); err != nil {
		return nil, err
	}
	return next, nil
}

// Update persists next, switches the interface language and restarts the check
// loop with the new settings, then runs one check immediately so the user sees
// the effect without waiting for the next tick.
func (a *appState) Update(next *Config) error {
	if err := next.Save(); err != nil {
		return err
	}
	SetLang(Lang(next.Language))
	Log.SetVerbose(next.VerboseLog)

	a.mu.Lock()
	a.cfg = next
	svc := a.svc
	started := a.started
	a.mu.Unlock()

	if started {
		svc.Restart(next)
		svc.Trigger()
	}
	return nil
}

// LoginTest captures a one shot login attempt, which both the settings page
// and the "login" command report to the user.
type LoginTest struct {
	Before Status
	Result LoginResult
	After  Status
}

// TestLogin submits the configured credentials once. It reports what the
// portal replied and never logs the session out.
func (a *appState) TestLogin() LoginTest {
	cfg := a.Config()
	client := NewClient(cfg)
	before := client.QueryStatus()
	return LoginTest{
		Before: before,
		Result: client.Login(before),
		After:  client.QueryStatus(),
	}
}
