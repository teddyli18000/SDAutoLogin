//go:build (linux || darwin) && !notray

package tray

import (
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/systray"

	"sdautologin/internal/app"
	"sdautologin/internal/brand"
	"sdautologin/internal/i18n"
	"sdautologin/internal/logging"
	"sdautologin/internal/platform"
	"sdautologin/internal/service"
)

// HasTray reports whether this build puts up a tray icon. A build that says no
// is made for the machines that have no desktop session to put one in.
const HasTray = true

// Run provides the Linux / macOS tray through fyne.io/systray and blocks until it
// quits. On Linux that is a pure Go D-Bus StatusNotifierItem; a machine with no
// session bus has no tray, so the caller's fallback is taken here.
func Run(state *app.AppState, opts Options, fallback func(*app.Server)) {
	if err := sessionBusError(); err != nil {
		logging.Log.Warn("no session bus to put a tray icon on (%v), running without one", err)
		// No server was built here: the toolkit was to build one once it had an
		// icon to go with it, so the fallback has to make its own.
		fallback(nil)
		return
	}

	// The tap handler is registered before the event loop starts: the Linux
	// backend reads it while it exports the item, so one set from the ready
	// callback can arrive too late and leave the item with a menu but no clicks.
	var tray atomic.Pointer[unixTray]
	systray.SetOnTapped(func() {
		if t := tray.Load(); t != nil {
			logging.Log.Debug("tray: tapped, opening the settings page")
			t.openSettings()
		}
	})

	systray.Run(func() {
		tray.Store(trayReady(state, opts))
	}, func() {
		state.Stop()
		logging.Log.Info("tray stopped")
	})
}

type unixTray struct {
	state    *app.AppState
	settings *app.Server
	opts     Options

	mu      sync.Mutex
	mStatus *systray.MenuItem
	mSince  *systray.MenuItem

	iconOnline  []byte
	iconOffline []byte
	iconBusy    []byte
	iconError   []byte
	lastState   service.State
	// lastRefused is whether the snapshot before this one was one the portal had
	// refused to move the session for, so the reader is told about it once.
	lastRefused bool
	// alert decides which failed rounds are worth a notification, since one
	// outage passes through LoggingIn between two errors.
	alert outageAlert
}

func trayReady(state *app.AppState, opts Options) *unixTray {
	t := &unixTray{
		state:       state,
		opts:        opts,
		lastState:   service.StateUnknown,
		iconOnline:  brand.PNG(22, iconAccent(service.StateOnline)),
		iconOffline: brand.PNG(22, iconAccent(service.StateUnknown)),
		iconBusy:    brand.PNG(22, iconAccent(service.StateLoggingIn)),
		iconError:   brand.PNG(22, iconAccent(service.StateError)),
	}
	t.settings = app.NewServer(state, opts.SettingsAddr, func() {
		systray.Quit()
	})

	systray.SetIcon(t.iconOffline)
	systray.SetTooltip(i18n.L("app.name") + " - " + stateLabel(service.StateUnknown))

	t.mStatus = systray.AddMenuItem(i18n.Lf("tray.status", stateLabel(service.StateUnknown)), "")
	t.mStatus.Disable()
	t.mSince = systray.AddMenuItem("", "")
	t.mSince.Hide()

	systray.AddSeparator()
	mNow := systray.AddMenuItem(i18n.L("tray.checkNow"), "")
	mSettings := systray.AddMenuItem(i18n.L("tray.settings"), "")
	systray.AddSeparator()
	mAutostart := systray.AddMenuItemCheckbox(i18n.L("tray.autostart"), "", platform.AutostartEnabled())
	mReload := systray.AddMenuItem(i18n.L("tray.reload"), "")
	mOpenCfg := systray.AddMenuItem(i18n.L("tray.openConfig"), "")
	mOpenLog := systray.AddMenuItem(i18n.L("tray.openLog"), "")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem(i18n.L("tray.exit"), "")

	go func() {
		for range mNow.ClickedCh {
			logging.Log.Info("manual check requested from the tray menu")
			t.state.Trigger()
		}
	}()
	go func() {
		for range mSettings.ClickedCh {
			logging.Log.Debug("tray: settings clicked")
			t.openSettings()
		}
	}()
	go func() {
		for range mReload.ClickedCh {
			logging.Log.Debug("tray: reload config clicked")
			if next, err := t.state.Reload(); err != nil {
				logging.Log.Error("reloading config: %v", err)
			} else {
				logging.Log.Info("config reloaded: %s", next.Summary())
			}
		}
	}()
	go func() {
		for range mOpenCfg.ClickedCh {
			logging.Log.Debug("tray: open config file clicked")
			if err := platform.OpenExternal(t.state.Config().Path()); err != nil {
				logging.Log.Warn("opening the config file: %v", err)
			}
		}
	}()
	go func() {
		for range mOpenLog.ClickedCh {
			logging.Log.Debug("tray: open log clicked")
			if p := logging.Log.Path(); p != "" {
				if err := platform.OpenExternal(p); err != nil {
					logging.Log.Warn("opening the log file: %v", err)
				}
			}
		}
	}()
	go func() {
		for range mAutostart.ClickedCh {
			on := !mAutostart.Checked()
			logging.Log.Debug("tray: autostart toggle clicked, now %v", on)
			if !platform.SetAutostart(on) {
				logging.Log.Warn("updating autostart entry failed")
				continue
			}
			if on {
				mAutostart.Check()
			} else {
				mAutostart.Uncheck()
			}
			logging.Log.Info("autostart set to %v", on)
		}
	}()
	go func() {
		<-mQuit.ClickedCh
		logging.Log.Info("exit requested from the tray menu")
		systray.Quit()
	}()

	t.state.SetListener(t.onStatus)
	t.state.Start()

	// A second start is a request for the settings page, which the running copy
	// answers through the socket published beside its lock file. The listener is
	// already bound, so a request waits in the socket until the icon is ready.
	go platform.WaitForSettingsRequest(t.answerSettingsRequest)

	// A page opened for a machine with nothing written down yet is logged by the
	// caller, which knows whether the page was opened by this or by the fallback.
	if !t.state.Config().HasAccounts() {
		t.openSettings()
	} else if t.opts.AppWindow {
		t.openSettings()
	}
	logging.Log.Debug("tray ready, menu items registered")
	return t
}

// answerSettingsRequest opens the settings page because a later copy of the
// program asked for it. It runs on the goroutine that reads the socket, and
// touches nothing the tray mutex guards.
func (t *unixTray) answerSettingsRequest() {
	logging.Log.Info("opening the settings page for another copy that asked for it")
	t.openSettings()
}

// openSettings serves the settings page and hands it to the browser, in a window
// of its own when that was asked for.
func (t *unixTray) openSettings() {
	open := t.settings.OpenURL
	if t.opts.AppWindow {
		open = t.settings.OpenAppWindow
	}
	if _, err := open(); err != nil {
		logging.Log.Error("opening the settings page: %v", err)
	}
	// The address handed to the browser carries the one time token, and a log
	// line outlives the page: the port is what a log can be told.
	if addr, ok := t.settings.Addr(); ok {
		logging.Log.Debug("settings page opened on %s", addr)
	}
}

func (t *unixTray) onStatus(s service.Snapshot) {
	t.mu.Lock()
	previous := t.lastState
	t.lastState = s.State
	refused := s.SwitchRefused != nil
	previousRefused := t.lastRefused
	t.lastRefused = refused

	if s.State != previous {
		logging.Log.Debug("tray icon updated to %s (internet checked=%v ok=%v)", s.State, s.InternetChecked, s.InternetOK)
		switch s.State {
		case service.StateOnline:
			systray.SetIcon(t.iconOnline)
		case service.StateLoggingIn, service.StateOffline:
			systray.SetIcon(t.iconBusy)
		case service.StateError:
			systray.SetIcon(t.iconError)
		default:
			systray.SetIcon(t.iconOffline)
		}
	}
	systray.SetTooltip(t.tooltip(s))
	t.mStatus.SetTitle(i18n.Lf("tray.status", stateLabel(s.State)))
	if s.LastLoginAt != "" {
		t.mSince.SetTitle(i18n.Lf("tray.lastLogin", s.LastLoginAt))
		t.mSince.Show()
	}
	t.mu.Unlock()

	// The transitions the reader is told about, as the Windows tray does with a
	// balloon tip. The alert is asked first: it has to see every snapshot. It runs
	// outside the lock, since a notification starts a helper process.
	failed := t.alert.note(s, time.Now())
	switch {
	case previous == service.StateLoggingIn && s.State == service.StateOnline:
		platform.Notify(i18n.L("app.name"), i18n.L("balloon.loggedIn"))
	case failed:
		platform.Notify(i18n.L("app.name"), i18n.L("balloon.reconnectFailed")+": "+s.Message)
	case refused && !previousRefused:
		logging.Log.Debug("tray: the session could not be moved onto the service type its entry puts first, saying so once")
		platform.Notify(i18n.L("app.name"), i18n.L("balloon.switchRefused"))
	}
}

func (t *unixTray) tooltip(s service.Snapshot) string {
	out := i18n.L("app.name") + "\n" + i18n.Lf("tray.status", stateLabel(s.State))
	if s.Message != "" && s.State == service.StateError {
		out += " (" + s.Message + ")"
	}
	// The session is up and the program could not move it where the order of the
	// login list says, so the reader is told it is theirs to move. It is a line of
	// its own because the state is Online: there is no error, only something to do.
	if s.SwitchRefused != nil {
		out += "\n" + i18n.L("tray.switchRefused")
	}
	// A release the reader could move to, where a desktop reader notices it.
	out += updateLine(s)
	if s.ClientIP != "" {
		out += "\n" + i18n.Lf("tray.clientIP", s.ClientIP)
	}
	if s.InternetChecked {
		if s.InternetOK {
			out += "\n" + i18n.L("tray.internetOk")
		} else {
			out += "\n" + i18n.L("tray.internetFail")
		}
	}
	return out
}
