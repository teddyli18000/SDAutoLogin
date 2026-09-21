//go:build (linux || darwin) && !notray

package tray

import (
	"sync"
	"sync/atomic"

	"fyne.io/systray"

	"sdautologin/internal/app"
	"sdautologin/internal/brand"
	"sdautologin/internal/i18n"
	"sdautologin/internal/logging"
	"sdautologin/internal/platform"
	"sdautologin/internal/service"
)

// HasTray reports whether this build puts up a tray icon at all. A build that
// says no is made for the machines that have no desktop session to put one in.
const HasTray = true

// Run provides the Linux / macOS tray through fyne.io/systray, since the
// standard library cannot create one, and blocks until the tray quits. On Linux
// the library uses a pure Go D-Bus StatusNotifierItem backend, on macOS it needs
// cgo and therefore the Xcode command line tools at build time.
//
// There is no fallback here: the toolkit either puts the icon up or the program
// exits with it, so the caller's fallback is unused.
func Run(state *app.AppState, opts Options, _ func()) {
	// The tap handler is registered before the event loop starts on purpose.
	// The Linux backend reads it while it exports the status notifier item, so a
	// handler set from the ready callback can arrive too late: the item would be
	// marked as menu only and the desktop would never report a click.
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

	if !t.state.Config().Configured() {
		logging.Log.Warn("no account configured yet, opening the settings page")
		t.openSettings()
	} else if t.opts.AppWindow {
		t.openSettings()
	}
	logging.Log.Debug("tray ready, menu items registered")
	return t
}

// openSettings serves the settings page and hands it to the browser, in a window
// of its own when that was asked for.
func (t *unixTray) openSettings() {
	open := t.settings.OpenURL
	if t.opts.AppWindow {
		open = t.settings.OpenAppWindow
	}
	url, err := open()
	if err != nil {
		logging.Log.Error("opening the settings page: %v", err)
	}
	logging.Log.Debug("settings page at %s", url)
}

func (t *unixTray) onStatus(s service.Snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if s.State != t.lastState {
		t.lastState = s.State
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
}

func (t *unixTray) tooltip(s service.Snapshot) string {
	out := i18n.L("app.name") + "\n" + i18n.Lf("tray.status", stateLabel(s.State))
	if s.Message != "" && s.State == service.StateError {
		out += " (" + s.Message + ")"
	}
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
