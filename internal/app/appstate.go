package app

import (
	"sync"

	"sdautologin/internal/config"
	"sdautologin/internal/i18n"
	"sdautologin/internal/logging"
	"sdautologin/internal/portal"
	"sdautologin/internal/service"
)

// AppState owns the mutable configuration and the running service, so the
// tray, the settings service and the command line all read and write the same
// values instead of keeping private copies.
type AppState struct {
	mu      sync.Mutex
	cfg     *config.Config
	svc     *service.Service
	started bool
}

// New returns the shared state around an already loaded configuration.
func New(cfg *config.Config) *AppState {
	// The file the passwords are in is looked at here rather than on every save,
	// since what can be loose about it is not something this program just wrote:
	// it is a file an older version made, or a copy somebody moved around.
	if report := cfg.Protect(); report != "" {
		logging.Log.Warn("%s", report)
	}
	return &AppState{cfg: cfg, svc: service.NewService(cfg)}
}

func (a *AppState) Config() *config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// Snapshot returns the latest status shown to the user.
func (a *AppState) Snapshot() service.Snapshot {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	return svc.Snapshot()
}

// Start begins the periodic check, once.
func (a *AppState) Start() {
	a.mu.Lock()
	svc, first := a.svc, !a.started
	a.started = true
	a.mu.Unlock()
	if first {
		svc.Start()
	}
}

func (a *AppState) Stop() {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	svc.Stop()
}

func (a *AppState) Trigger() {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	svc.Trigger()
}

func (a *AppState) SetListener(f func(service.Snapshot)) {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	svc.SetListener(f)
}

// Reload re-reads config.json, applies it and returns the new configuration.
func (a *AppState) Reload() (*config.Config, error) {
	next, err := config.LoadConfig(a.Config().Path())
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
func (a *AppState) Update(next *config.Config) error {
	if err := next.Save(); err != nil {
		return err
	}
	// A save does not fail over the file's access list, so what could not be put
	// right about it is said here instead.
	if report := next.Protect(); report != "" {
		logging.Log.Warn("%s", report)
	}
	i18n.SetLang(i18n.Lang(next.Language))
	logging.Log.SetVerbose(next.VerboseLog)

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
	// Attempt is the attempt that was submitted, so the report can name the
	// account and the service type it went out with.
	Attempt config.Attempt
	Before  portal.Status
	Result  portal.LoginResult
	After   portal.Status
	// AlreadyOnline says the portal reported this client as signed in before
	// anything was submitted, which is the one case where nothing is.
	AlreadyOnline bool
}

// TestLogin submits one attempt of the login list. It reports what the portal
// replied and never logs the session out.
//
// A client the portal already reports as signed in is left alone: the portal
// allows one session per address, so a second login on top of it comes back as
// a refusal that blames the account, which says nothing about the credentials.
func (a *AppState) TestLogin(attempt config.Attempt) LoginTest {
	cfg := a.Config()
	client := portal.NewClient(cfg)
	before := client.QueryStatus()
	if before.Online {
		return LoginTest{Attempt: attempt, Before: before, After: before, AlreadyOnline: true}
	}
	return LoginTest{
		Attempt: attempt,
		Before:  before,
		Result:  client.Login(before, attempt),
		After:   client.QueryStatus(),
	}
}

// SwitchService signs in again with one service type of the entry the session is
// on, while the session is up on another one, which is what the settings page's
// button asks for: the first service type of the entry by default, or another one
// the reader picks when the first is the one the portal will not take. The portal
// keeps one session per client address, so this replaces the session or is
// refused; either way nothing is logged out on purpose, and the second sign-in is
// what the session is left to decide.
//
// The second return value says whether there was anything to do. A session that
// is already down, one that is on the service type asked for already, and a
// service type the entry does not list all have nothing to do here, and the page
// says that instead of showing a login reply that was never asked for.
func (a *AppState) SwitchService(isp string) (LoginTest, bool) {
	cfg := a.Config()
	client := portal.NewClient(cfg)
	before := client.QueryStatus()
	if !before.Online {
		// Nothing to move: the session is down, and the next check signs in on
		// its own - in the order of the list, which is the first service type.
		return LoginTest{Before: before, After: before}, false
	}
	attempt, ok := cfg.SwitchTo(before.Account, isp)
	if !ok {
		return LoginTest{Before: before, After: before}, false
	}
	result := client.Login(before, attempt)
	return LoginTest{
		Attempt: attempt,
		Before:  before,
		Result:  result,
		After:   client.QueryStatus(),
	}, true
}
