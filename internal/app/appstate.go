package app

import (
	"context"
	"os"
	"sync"

	"sdautologin/internal/config"
	"sdautologin/internal/i18n"
	"sdautologin/internal/logging"
	"sdautologin/internal/portal"
	"sdautologin/internal/service"
	"sdautologin/internal/update"
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
	// The password file was read and checked while the configuration was loaded,
	// and this reports what was loose about the files themselves: what is loose
	// did not come from this program. It is an older version's file, or a copy
	// somebody moved around.
	if report := cfg.Protect(); report != "" {
		logging.Log.Warn("%s", report)
	}
	// What reading the password file had to say, reported once for the same
	// reason: it is a fact about the deployment, not about a check.
	if notice := cfg.PasswordNotice(); notice != "" {
		logging.Log.Warn("%s", notice)
	}
	return &AppState{cfg: cfg, svc: newService(cfg)}
}

// newService is the check loop with this copy's look for a newer release: the
// repository address, the version this build carries and the program file to
// replace are all known here, and internal/service cannot import this package.
func newService(cfg *config.Config) *service.Service {
	svc := service.NewService(cfg)
	svc.SetUpdater(newUpdater())
	return svc
}

// updater is what internal/service asks for a newer release: the forge this
// project is published on, the version this build is, and the program file a
// release would go over.
type updater struct {
	client  *update.Client
	current update.Version
	target  update.Target
}

// newUpdater builds one, or answers nil when this build cannot say what version
// it is: every release would look newer than a version that cannot be compared,
// so the forge is left alone and the reason is logged once.
func newUpdater() service.Updater {
	current, err := update.ParseVersion(Version)
	if err != nil {
		logging.Log.Warn("not looking for a newer release: %v", err)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		logging.Log.Warn("not looking for a newer release, the program file is unknown: %v",
			err)
		return nil
	}
	return &updater{
		client:  UpdateClient(),
		current: current,
		target:  update.Native(exe),
	}
}

func (u *updater) Check(ctx context.Context, ch update.Channel) (update.Check, error) {
	return u.client.Check(ctx, ch, u.current, u.target)
}

func (u *updater) Install(ctx context.Context, rel update.Release) (string, error) {
	return u.client.Install(ctx, rel, u.target)
}

func (a *AppState) Config() *config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

func (a *AppState) Snapshot() service.Snapshot {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	return svc.Snapshot()
}

// Start starts the check loop. Later calls do nothing.
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

// CheckForUpdateNow asks for a newer release at once, at the reader's request
// from the settings page.
func (a *AppState) CheckForUpdateNow() bool {
	return a.svc.CheckForUpdateNow()
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
	// The save has already put the files' permissions right, and fails when it
	// cannot: this reports what is still loose about the ones that were already
	// there, which a save cannot fail over.
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
		// Restart ticks as soon as its loop starts, so asking for a check as well
		// would run two of them back to back and submit a failing account twice
		// inside a second.
		svc.Restart(next)
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
	// anything was submitted. Nothing is submitted in that case.
	AlreadyOnline bool
}

// TestLogin submits one attempt of the login list and reports the reply, without
// logging the session out. A client already reported as signed in is left alone:
// the portal keeps one session per address, so a second login is refused.
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

// SwitchService signs the session in again with one service type of the entry it
// is on while it is up on another, and never logs the session out. The page's
// button asks for the first service type, or another one the reader picks.
func (a *AppState) SwitchService(isp string) (LoginTest, bool) {
	cfg := a.Config()
	client := portal.NewClient(cfg)
	before := client.QueryStatus()
	if !before.Online {
		// The session is down, so there is nothing to move: the next check signs
		// in on its own, in the order of the list.
		logging.Log.Info("nothing to sign in again as %s: the session is down (client ip %q, %s), the next check signs in on its own",
			config.OperatorName(isp), before.ClientIP, before.Message)
		return LoginTest{Before: before, After: before}, false
	}
	index, used := cfg.Match(before.Account)
	attempt, ok := cfg.SwitchTo(before.Account, isp)
	if !ok {
		// There is nothing to move, and the log names which case it is: the portal
		// account matches no entry of the login list, the session is on that
		// service type already, or the entry does not list it.
		switch {
		case index < 0:
			logging.Log.Info("nothing to sign in again as %s: the portal reports the session on %q, which names no entry of the login list",
				config.OperatorName(isp), before.Account)
		case used == isp:
			logging.Log.Info("nothing to sign in again as %s: the session is on that service type already",
				config.OperatorName(isp))
		default:
			logging.Log.Info("nothing to sign in again as %s: entry %d is on %s and does not list that service type",
				config.OperatorName(isp), index+1, config.OperatorName(used))
		}
		return LoginTest{Before: before, After: before}, false
	}
	logging.Log.Info("signing in again while the session is up, to move it from %s onto %s (entry %d)",
		config.OperatorName(used), config.OperatorName(attempt.ISP), index+1)
	result := client.Login(before, attempt)
	return LoginTest{
		Attempt: attempt,
		Before:  before,
		Result:  result,
		After:   client.QueryStatus(),
	}, true
}
