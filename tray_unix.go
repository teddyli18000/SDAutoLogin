//go:build linux || darwin

package main

import (
	"sync"

	"fyne.io/systray"

	"sdautologin/internal/brand"
)

// runUI provides the Linux / macOS tray through fyne.io/systray, since the
// standard library cannot create one. On Linux the library uses a pure Go
// D-Bus StatusNotifierItem backend, on macOS it needs cgo and therefore the
// Xcode command line tools at build time.
func runUI(state *appState, appDir string) {
	systray.Run(func() {
		trayReady(state, appDir)
	}, func() {
		state.Stop()
		Log.Info("tray stopped")
	})
}

type unixTray struct {
	state    *appState
	settings *settingsServer

	mu      sync.Mutex
	mStatus *systray.MenuItem
	mSince  *systray.MenuItem

	iconOnline  []byte
	iconOffline []byte
	iconBusy    []byte
	iconError   []byte
	lastState   State
}

func trayReady(state *appState, appDir string) {
	t := &unixTray{
		state:       state,
		lastState:   StateUnknown,
		iconOnline:  brand.PNG(22, iconAccent(StateOnline)),
		iconOffline: brand.PNG(22, iconAccent(StateUnknown)),
		iconBusy:    brand.PNG(22, iconAccent(StateLoggingIn)),
		iconError:   brand.PNG(22, iconAccent(StateError)),
	}
	t.settings = newSettingsServer(state, appDir, func() {
		systray.Quit()
	})

	systray.SetIcon(t.iconOffline)
	systray.SetTooltip(L("app.name") + " - " + localizedState(StateUnknown))

	t.mStatus = systray.AddMenuItem(Lf("tray.status", localizedState(StateUnknown)), "")
	t.mStatus.Disable()
	t.mSince = systray.AddMenuItem("", "")
	t.mSince.Hide()

	systray.AddSeparator()
	mNow := systray.AddMenuItem(L("tray.checkNow"), "")
	mSettings := systray.AddMenuItem(L("tray.settings"), "")
	systray.AddSeparator()
	mAutostart := systray.AddMenuItemCheckbox(L("tray.autostart"), "", autostartEnabled())
	mReload := systray.AddMenuItem(L("tray.reload"), "")
	mOpenCfg := systray.AddMenuItem(L("tray.openConfig"), "")
	mOpenLog := systray.AddMenuItem(L("tray.openLog"), "")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem(L("tray.exit"), "")

	go func() {
		for range mNow.ClickedCh {
			Log.Info("manual check requested from the tray menu")
			t.state.Trigger()
		}
	}()
	go func() {
		for range mSettings.ClickedCh {
			Log.Debug("tray: settings clicked")
			t.openSettings()
		}
	}()
	go func() {
		for range mReload.ClickedCh {
			Log.Debug("tray: reload config clicked")
			if next, err := t.state.Reload(); err != nil {
				Log.Error("reloading config: %v", err)
			} else {
				Log.Info("config reloaded: %s", next.Summary())
			}
		}
	}()
	go func() {
		for range mOpenCfg.ClickedCh {
			Log.Debug("tray: open config file clicked")
			if err := openExternal(t.state.Config().Path()); err != nil {
				Log.Warn("opening the config file: %v", err)
			}
		}
	}()
	go func() {
		for range mOpenLog.ClickedCh {
			Log.Debug("tray: open log clicked")
			if p := Log.Path(); p != "" {
				if err := openExternal(p); err != nil {
					Log.Warn("opening the log file: %v", err)
				}
			}
		}
	}()
	go func() {
		for range mAutostart.ClickedCh {
			on := !mAutostart.Checked()
			Log.Debug("tray: autostart toggle clicked, now %v", on)
			if !setAutostart(on) {
				Log.Warn("updating autostart entry failed")
				continue
			}
			if on {
				mAutostart.Check()
			} else {
				mAutostart.Uncheck()
			}
			Log.Info("autostart set to %v", on)
		}
	}()
	go func() {
		<-mQuit.ClickedCh
		Log.Info("exit requested from the tray menu")
		systray.Quit()
	}()

	t.state.SetListener(t.onStatus)
	t.state.Start()

	if !t.state.Config().Configured() {
		Log.Warn("no account configured yet, opening the settings page")
		t.openSettings()
	}
	Log.Debug("tray ready, menu items registered")
}

func (t *unixTray) openSettings() {
	url, err := t.settings.OpenURL()
	if err != nil {
		Log.Error("opening the settings page: %v", err)
	}
	Log.Debug("settings page at %s", url)
}

func (t *unixTray) onStatus(s Snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if s.State != t.lastState {
		t.lastState = s.State
		Log.Debug("tray icon updated to %s (internet checked=%v ok=%v)", s.State, s.InternetChecked, s.InternetOK)
		switch s.State {
		case StateOnline:
			systray.SetIcon(t.iconOnline)
		case StateLoggingIn, StateOffline:
			systray.SetIcon(t.iconBusy)
		case StateError:
			systray.SetIcon(t.iconError)
		default:
			systray.SetIcon(t.iconOffline)
		}
	}
	systray.SetTooltip(t.tooltip(s))
	t.mStatus.SetTitle(Lf("tray.status", localizedState(s.State)))
	if s.LastLoginAt != "" {
		t.mSince.SetTitle(Lf("tray.lastLogin", s.LastLoginAt))
		t.mSince.Show()
	}
}

func (t *unixTray) tooltip(s Snapshot) string {
	out := L("app.name") + "\n" + Lf("tray.status", localizedState(s.State))
	if s.Message != "" && s.State == StateError {
		out += " (" + s.Message + ")"
	}
	if s.ClientIP != "" {
		out += "\n" + Lf("tray.clientIP", s.ClientIP)
	}
	if s.InternetChecked {
		if s.InternetOK {
			out += "\n" + L("tray.internetOk")
		} else {
			out += "\n" + L("tray.internetFail")
		}
	}
	return out
}
