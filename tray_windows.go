//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"sdautologin/internal/brand"
)

// Message ids posted to the hidden tray window.
const (
	wmTrayIcon      = wmApp + 1
	wmTrayRefresh   = wmApp + 2
	wmQuitRequested = wmApp + 3
)

// Tray menu command ids.
const (
	cmdCheckNow  = 1001
	cmdSettings  = 1002
	cmdReload    = 1003
	cmdOpenCfg   = 1004
	cmdAutostart = 1005
	cmdOpenLog   = 1006
	cmdExit      = 1007
)

const (
	mbIconError   = 0x10
	mbIconWarning = 0x30
)

type trayApp struct {
	state    *appState
	appDir   string
	settings *settingsServer

	hwnd syscall.Handle
	nid  notifyIconData
	// showSettingsMsg is the id a second copy posts to ask for the settings
	// page, zero when the system could not allocate one.
	showSettingsMsg uint32

	mu   sync.Mutex
	snap Snapshot

	iconChecking syscall.Handle
	iconOnline   syscall.Handle
	iconBusy     syscall.Handle
	iconError    syscall.Handle

	// lastState is only touched by the UI thread.
	lastState State
}

var trayInstance *trayApp

var trayWndProc = syscall.NewCallback(trayProc)

func trayProc(hwnd, umsg, wparam, lparam uintptr) uintptr {
	if trayInstance != nil && trayInstance.showSettingsMsg != 0 &&
		uint32(umsg) == trayInstance.showSettingsMsg {
		Log.Debug("tray: a second copy asked for the settings page")
		trayInstance.openSettings()
		return 0
	}
	switch uint32(umsg) {
	case wmTrayIcon:
		if trayInstance == nil {
			break
		}
		mouse := uint32(lparam) & 0xffff
		switch mouse {
		case wmLButtonUp:
			// A primary click opens the settings page, the way the instant
			// messengers this program mimics behave. The double click that
			// follows a click is deliberately ignored, otherwise the page
			// would be opened twice.
			Log.Debug("tray: click, opening the settings page")
			trayInstance.openSettings()
		case wmRButtonUp:
			Log.Debug("tray: menu requested (mouse message %#x)", mouse)
			trayInstance.showMenu()
		}
		return 0
	case wmTrayRefresh:
		if trayInstance != nil {
			trayInstance.refresh()
		}
		return 0
	case wmQuitRequested:
		if trayInstance != nil {
			trayInstance.exit()
		}
		return 0
	case wmDestroy:
		Log.Debug("tray window destroyed")
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, umsg, wparam, lparam)
	return r
}

func runUI(state *appState, appDir string) {
	// The tray window and its message loop must stay on one OS thread.
	runtime.LockOSThread()

	app := &trayApp{state: state, appDir: appDir}
	trayInstance = app
	app.showSettingsMsg = showSettingsMessage()
	app.settings = newSettingsServer(state, appDir, func() {
		// DestroyWindow has to run on the thread that created the window.
		procPostMessageW.Call(uintptr(app.hwnd), wmQuitRequested, 0, 0)
	})

	if err := app.create(); err != nil {
		Log.Error("creating the tray icon: %v, falling back to background mode", err)
		runHeadless(state, appDir)
		return
	}

	state.SetListener(app.onStatus)
	state.Start()
	app.refresh()

	if !state.Config().Configured() {
		app.openSettings()
	}

	Log.Debug("entering the tray message loop")
	app.messageLoop()
	state.Stop()
	app.settings.Stop()
	Log.Info("tray stopped")
}

func (a *trayApp) create() error {
	const className = "SDAutoLoginTrayWnd"
	wc := wndClassExW{
		LpfnWndProc:   trayWndProc,
		HInstance:     syscall.Handle(moduleHandle()),
		LpszClassName: u16ptr(className),
		HCursor:       loadSystemCursor(),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassExW: %v", err)
	}

	// A never shown top level window is enough to receive the tray callbacks.
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(u16ptr(className))),
		uintptr(unsafe.Pointer(u16ptr(appTitle))),
		0, 0, 0, 0, 0,
		0, 0, moduleHandle(), 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %v", err)
	}
	a.hwnd = syscall.Handle(hwnd)

	size := trayIconSize()
	a.iconChecking = makeIcon(brand.Icon(size, iconAccent(StateUnknown)))
	a.iconOnline = makeIcon(brand.Icon(size, iconAccent(StateOnline)))
	a.iconBusy = makeIcon(brand.Icon(size, iconAccent(StateLoggingIn)))
	a.iconError = makeIcon(brand.Icon(size, iconAccent(StateError)))
	if a.iconChecking == 0 || a.iconOnline == 0 || a.iconBusy == 0 || a.iconError == 0 {
		return fmt.Errorf("could not build the tray icons")
	}

	a.nid = notifyIconData{
		HWnd:             a.hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: wmTrayIcon,
		HIcon:            a.iconChecking,
	}
	a.nid.CbSize = uint32(unsafe.Sizeof(a.nid))
	putUTF16(a.nid.SzTip[:], L("app.name")+" - "+localizedState(StateUnknown))
	if r, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&a.nid))); r == 0 {
		return fmt.Errorf("Shell_NotifyIcon: %v", err)
	}
	Log.Info("tray icon created (%d px)", size)
	return nil
}

func (a *trayApp) messageLoop() {
	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// onStatus is called from the worker goroutine. It only stores the snapshot
// and wakes the UI thread.
func (a *trayApp) onStatus(s Snapshot) {
	a.mu.Lock()
	a.snap = s
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmTrayRefresh, 0, 0)
}

func (a *trayApp) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snap
}

func (a *trayApp) refresh() {
	s := a.Snapshot()
	previous := a.lastState
	a.lastState = s.State

	a.nid.HIcon = a.iconFor(s.State)
	a.nid.UFlags = nifMessage | nifIcon | nifTip
	putUTF16(a.nid.SzTip[:], a.tooltip(s))
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&a.nid)))

	if s.State != previous {
		Log.Debug("tray icon updated to %s (internet checked=%v ok=%v)",
			s.State, s.InternetChecked, s.InternetOK)
	}
	// Tell the user about the two transitions that matter; staying quiet about
	// the rest keeps balloon tips from becoming noise.
	switch {
	case previous == StateLoggingIn && s.State == StateOnline:
		a.notify(L("app.name"), L("balloon.loggedIn"))
	case s.State == StateError && previous != StateError:
		a.notify(L("app.name"), L("balloon.reconnectFailed")+": "+s.Message)
	}
}

// notify shows a balloon tip through the existing tray icon.
func (a *trayApp) notify(title, body string) {
	a.nid.UFlags = nifMessage | nifIcon | nifTip | nifInfo
	a.nid.DwInfoFlags = niiInfo
	putUTF16(a.nid.SzInfoTitle[:], title)
	putUTF16(a.nid.SzInfo[:], body)
	putUTF16(a.nid.SzTip[:], a.tooltip(a.Snapshot()))
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&a.nid)))
	a.nid.UFlags = nifMessage | nifIcon | nifTip
}

func (a *trayApp) iconFor(st State) syscall.Handle {
	switch st {
	case StateOnline:
		return a.iconOnline
	case StateLoggingIn, StateOffline:
		return a.iconBusy
	case StateError:
		return a.iconError
	default:
		return a.iconChecking
	}
}

func (a *trayApp) tooltip(s Snapshot) string {
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

func (a *trayApp) showMenu() {
	s := a.Snapshot()
	Log.Debug("building the tray menu for state %s", s.State)
	menu, _, _ := procCreatePopupMenu.Call()

	appendMenu(menu, mfString|mfGrayed, 0, Lf("tray.status", localizedState(s.State)))
	if s.LastLoginAt != "" {
		appendMenu(menu, mfString|mfGrayed, 0, Lf("tray.lastLogin", s.LastLoginAt))
	}
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, cmdCheckNow, L("tray.checkNow"))
	appendMenu(menu, mfString, cmdSettings, L("tray.settings"))
	appendMenu(menu, mfSeparator, 0, "")
	if autostartEnabled() {
		appendMenu(menu, mfString|mfChecked, cmdAutostart, L("tray.autostart"))
	} else {
		appendMenu(menu, mfString, cmdAutostart, L("tray.autostart"))
	}
	appendMenu(menu, mfString, cmdReload, L("tray.reload"))
	appendMenu(menu, mfString, cmdOpenCfg, L("tray.openConfig"))
	appendMenu(menu, mfString, cmdOpenLog, L("tray.openLog"))
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, cmdExit, L("tray.exit"))

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(uintptr(a.hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(menu,
		tpmRightButton|tpmReturnCmd,
		uintptr(pt.X), uintptr(pt.Y), 0, uintptr(a.hwnd), 0)
	procDestroyMenu.Call(menu)
	// Posted so the menu closes even when the user clicked outside of it.
	procPostMessageW.Call(uintptr(a.hwnd), 0, 0, 0)

	if cmd != 0 {
		a.handleCommand(uint32(cmd))
	}
}

func appendMenu(menu uintptr, flags uintptr, id uintptr, text string) {
	if text == "" {
		procAppendMenuW.Call(menu, flags, id, 0)
		return
	}
	procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(u16ptr(text))))
}

func (a *trayApp) handleCommand(cmd uint32) {
	Log.Debug("tray command selected: id=%d", cmd)
	switch cmd {
	case cmdCheckNow:
		Log.Info("manual check requested from the tray menu")
		a.state.Trigger()
	case cmdSettings:
		a.openSettings()
	case cmdReload:
		a.reloadConfig()
	case cmdOpenCfg:
		Log.Info("opening config file %s", a.state.Config().Path())
		openExternal(a.state.Config().Path())
	case cmdOpenLog:
		if p := Log.Path(); p != "" {
			Log.Info("opening log file %s", p)
			openExternal(p)
		}
	case cmdAutostart:
		on := !autostartEnabled()
		if setAutostart(on) {
			Log.Info("autostart set to %v", on)
		} else {
			messageBox(a.hwnd, L("app.name"), L("dlg.autostartFailed"), mbIconWarning)
		}
	case cmdExit:
		Log.Info("exit requested from the tray menu")
		a.exit()
	}
}

// openSettings serves the settings page and hands it to the default browser.
func (a *trayApp) openSettings() {
	url, err := a.settings.OpenURL()
	if err == nil {
		return
	}
	Log.Error("opening the settings page: %v", err)
	if url == "" {
		// No address to fall back to, the service itself did not start.
		messageBox(a.hwnd, L("dlg.title"), L("dlg.settingsFailOpen"), mbIconError)
		return
	}
	messageBox(a.hwnd, L("dlg.title"), Lf("dlg.startedBody", url), mbIconWarning)
}

func (a *trayApp) reloadConfig() {
	Log.Info("reloading config from %s", a.state.Config().Path())
	next, err := a.state.Reload()
	if err != nil {
		Log.Error("reloading config: %v", err)
		messageBox(a.hwnd, L("dlg.title"), L("err.loadFailed"), mbIconError)
		return
	}
	// The menu wording follows the newly selected language.
	a.refresh()
	Log.Info("config reloaded: %s", next.Summary())
}

func (a *trayApp) exit() {
	Log.Debug("removing the tray icon and destroying the tray window")
	a.settings.Stop()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&a.nid)))
	procDestroyWindow.Call(uintptr(a.hwnd))
}
