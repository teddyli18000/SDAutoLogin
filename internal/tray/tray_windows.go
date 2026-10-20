//go:build windows && !notray

package tray

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"sdautologin/internal/app"
	"sdautologin/internal/brand"
	"sdautologin/internal/i18n"
	"sdautologin/internal/logging"
	"sdautologin/internal/platform"
	"sdautologin/internal/service"
)

// trayWindowTitle is the title of the never shown window that receives the
// tray callbacks.
const trayWindowTitle = "Campus Auto Login"

// Message ids posted to the hidden tray window.
const (
	wmTrayIcon      = wmApp + 1
	wmTrayRefresh   = wmApp + 2
	wmQuitRequested = wmApp + 3
	wmShowSettings  = wmApp + 4
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
	state    *app.AppState
	settings *app.Server
	opts     Options

	hwnd syscall.Handle
	nid  notifyIconData

	// uiUp is set while the message loop runs. It tells the settings request
	// handler whether a click can be replayed as a message or the page opened
	// straight away, and it is the only field another goroutine reads.
	uiUp atomic.Bool

	mu   sync.Mutex
	snap service.Snapshot

	iconChecking syscall.Handle
	iconOnline   syscall.Handle
	iconBusy     syscall.Handle
	iconError    syscall.Handle

	// lastState, lastRefused, clicks and alert are only touched by the UI
	// thread. lastRefused is whether the snapshot before this one was one the
	// portal had refused to move the session for, so the reader is told about it
	// once.
	lastState   service.State
	lastRefused bool
	clicks      clickDebounce
	alert       outageAlert
}

var trayInstance *trayApp

var trayWndProc = syscall.NewCallback(trayProc)

func trayProc(hwnd, umsg, wparam, lparam uintptr) uintptr {
	switch uint32(umsg) {
	case wmTrayIcon:
		if trayInstance == nil {
			break
		}
		mouse := uint32(lparam) & 0xffff
		switch mouse {
		case wmLButtonUp:
			// A primary click opens the settings page, as instant messengers do.
			logging.Log.Debug("tray: click, opening the settings page")
			trayInstance.openSettingsFromClick()
		case wmRButtonUp:
			logging.Log.Debug("tray: menu requested (mouse message %#x)", mouse)
			trayInstance.showMenu()
		}
		return 0
	case wmShowSettings:
		if trayInstance != nil {
			logging.Log.Debug("tray: a second copy asked for the settings page")
			trayInstance.openSettings()
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
		logging.Log.Debug("tray window destroyed")
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, umsg, wparam, lparam)
	return r
}

// HasTray reports whether this build puts up a tray icon. Windows always has one;
// the builds that say no are the Linux ones made for machines with no desktop
// session.
const HasTray = true

// Run puts the tray icon up and blocks on the message loop. fallback is used when
// the tray cannot be created, so the program keeps checking in the background;
// it is handed the settings server, the only way in without an icon.
func Run(state *app.AppState, opts Options, fallback func(*app.Server)) {
	// The tray window and its message loop must stay on one OS thread.
	runtime.LockOSThread()

	a := &trayApp{state: state, opts: opts}
	trayInstance = a
	a.settings = app.NewServer(state, opts.SettingsAddr, func() {
		// DestroyWindow has to run on the thread that created the window.
		if hwnd := a.hwnd; hwnd != 0 {
			procPostMessageW.Call(uintptr(hwnd), wmQuitRequested, 0, 0)
		}
	})

	// Started before the icon exists: a request arriving while the tray is still
	// being built stays in the signalled event until the loop runs, and one after
	// a failed start is answered by opening the page directly.
	go platform.WaitForSettingsRequest(a.answerSettingsRequest)

	if err := a.create(); err != nil {
		logging.Log.Error("creating the tray icon: %v, falling back to background mode", err)
		// The half built window must not be offered as somewhere to post to.
		trayInstance = nil
		if a.opts.AppWindow {
			// The page does not need the tray, so the window asked for can still
			// be opened here.
			a.openSettings()
		}
		// The server goes with it: the page it serves is the way in on a machine
		// whose icon could not be made, and it has to be able to end this run.
		fallback(a.settings)
		return
	}

	state.SetListener(a.onStatus)
	state.Start()
	a.refresh()

	// Set before the loop starts: a message posted from here on is queued and
	// handled as soon as the loop runs.
	a.uiUp.Store(true)
	if a.opts.AppWindow || !state.Config().HasAccounts() {
		a.openSettings()
	}

	logging.Log.Debug("entering the tray message loop")
	a.messageLoop()
	a.uiUp.Store(false)
	trayInstance = nil
	state.Stop()
	a.settings.Stop()
	logging.Log.Info("tray stopped")
}

// answerSettingsRequest serves a request from a second copy. It runs on the
// waiter goroutine: with a message loop the click is replayed on the UI thread,
// otherwise the page is opened here, which needs no tray.
func (a *trayApp) answerSettingsRequest() {
	if a.uiUp.Load() {
		procPostMessageW.Call(uintptr(a.hwnd), wmShowSettings, 0, 0)
		return
	}
	logging.Log.Info("no tray loop to replay the request through, opening the settings page")
	a.openSettings()
}

func (a *trayApp) create() error {
	const className = "SDAutoLoginTrayWnd"
	wc := wndClassExW{
		LpfnWndProc:   trayWndProc,
		HInstance:     syscall.Handle(moduleHandle()),
		LpszClassName: platform.UTF16Ptr(className),
		HCursor:       loadSystemCursor(),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassExW: %v", err)
	}

	// A never shown top level window is enough to receive the tray callbacks.
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(platform.UTF16Ptr(className))),
		uintptr(unsafe.Pointer(platform.UTF16Ptr(trayWindowTitle))),
		0, 0, 0, 0, 0,
		0, 0, moduleHandle(), 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %v", err)
	}
	a.hwnd = syscall.Handle(hwnd)

	size := trayIconSize()
	a.iconChecking = makeIcon(brand.Icon(size, iconAccent(service.StateUnknown)))
	a.iconOnline = makeIcon(brand.Icon(size, iconAccent(service.StateOnline)))
	a.iconBusy = makeIcon(brand.Icon(size, iconAccent(service.StateLoggingIn)))
	a.iconError = makeIcon(brand.Icon(size, iconAccent(service.StateError)))
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
	putUTF16(a.nid.SzTip[:], i18n.L("app.name")+" - "+stateLabel(service.StateUnknown))
	if r, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&a.nid))); r == 0 {
		return fmt.Errorf("Shell_NotifyIcon: %v", err)
	}
	logging.Log.Info("tray icon created (%d px)", size)
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
func (a *trayApp) onStatus(s service.Snapshot) {
	a.mu.Lock()
	a.snap = s
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmTrayRefresh, 0, 0)
}

func (a *trayApp) Snapshot() service.Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snap
}

func (a *trayApp) refresh() {
	s := a.Snapshot()
	previous := a.lastState
	a.lastState = s.State
	refused := s.SwitchRefused != nil
	previousRefused := a.lastRefused
	a.lastRefused = refused

	a.nid.HIcon = a.iconFor(s.State)
	a.nid.UFlags = nifMessage | nifIcon | nifTip
	putUTF16(a.nid.SzTip[:], a.tooltip(s))
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&a.nid)))

	if s.State != previous {
		logging.Log.Debug("tray icon updated to %s (internet checked=%v ok=%v)",
			s.State, s.InternetChecked, s.InternetOK)
	}
	// Tell the reader about the transitions that matter; the rest would become
	// noise. The alert is asked first: it has to see every snapshot to tell
	// whether this is the same outage as the round before.
	failed := a.alert.note(s, time.Now())
	switch {
	case previous == service.StateLoggingIn && s.State == service.StateOnline:
		a.notify(i18n.L("app.name"), i18n.L("balloon.loggedIn"))
	case failed:
		a.notify(i18n.L("app.name"), i18n.L("balloon.reconnectFailed")+": "+s.Message)
	case refused && !previousRefused:
		logging.Log.Debug("tray: the session could not be moved onto the service type its entry puts first, saying so once")
		a.notify(i18n.L("app.name"), i18n.L("balloon.switchRefused"))
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

func (a *trayApp) iconFor(st service.State) syscall.Handle {
	switch st {
	case service.StateOnline:
		return a.iconOnline
	case service.StateLoggingIn, service.StateOffline:
		return a.iconBusy
	case service.StateError:
		return a.iconError
	default:
		return a.iconChecking
	}
}

func (a *trayApp) tooltip(s service.Snapshot) string {
	out := i18n.L("app.name") + "\n" + i18n.Lf("tray.status", stateLabel(s.State))
	if s.Message != "" && s.State == service.StateError {
		out += " (" + s.Message + ")"
	}
	// The session is up and the program could not move it where the order of the
	// login list says it should be, so the reader is told the session is theirs to
	// move. The line stands on its own: the state is Online, with no error to read.
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

func (a *trayApp) showMenu() {
	s := a.Snapshot()
	logging.Log.Debug("building the tray menu for state %s", s.State)
	menu, _, _ := procCreatePopupMenu.Call()

	appendMenu(menu, mfString|mfGrayed, 0, i18n.Lf("tray.status", stateLabel(s.State)))
	if s.LastLoginAt != "" {
		appendMenu(menu, mfString|mfGrayed, 0, i18n.Lf("tray.lastLogin", s.LastLoginAt))
	}
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, cmdCheckNow, i18n.L("tray.checkNow"))
	appendMenu(menu, mfString, cmdSettings, i18n.L("tray.settings"))
	appendMenu(menu, mfSeparator, 0, "")
	if platform.AutostartEnabled() {
		appendMenu(menu, mfString|mfChecked, cmdAutostart, i18n.L("tray.autostart"))
	} else {
		appendMenu(menu, mfString, cmdAutostart, i18n.L("tray.autostart"))
	}
	appendMenu(menu, mfString, cmdReload, i18n.L("tray.reload"))
	appendMenu(menu, mfString, cmdOpenCfg, i18n.L("tray.openConfig"))
	appendMenu(menu, mfString, cmdOpenLog, i18n.L("tray.openLog"))
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, cmdExit, i18n.L("tray.exit"))

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
	procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(platform.UTF16Ptr(text))))
}

func (a *trayApp) handleCommand(cmd uint32) {
	logging.Log.Debug("tray command selected: id=%d", cmd)
	switch cmd {
	case cmdCheckNow:
		logging.Log.Info("manual check requested from the tray menu")
		a.state.Trigger()
	case cmdSettings:
		a.openSettings()
	case cmdReload:
		a.reloadConfig()
	case cmdOpenCfg:
		logging.Log.Info("opening config file %s", a.state.Config().Path())
		platform.OpenExternal(a.state.Config().Path())
	case cmdOpenLog:
		if p := logging.Log.Path(); p != "" {
			logging.Log.Info("opening log file %s", p)
			platform.OpenExternal(p)
		}
	case cmdAutostart:
		on := !platform.AutostartEnabled()
		if platform.SetAutostart(on) {
			logging.Log.Info("autostart set to %v", on)
		} else {
			messageBox(a.hwnd, i18n.L("app.name"), i18n.L("dlg.autostartFailed"), mbIconWarning)
		}
	case cmdExit:
		logging.Log.Info("exit requested from the tray menu")
		a.exit()
	}
}

// openSettingsFromClick opens the settings page for a primary click. Windows
// sends down, up, double click, up for a double click, so a repeat inside the
// double click interval is treated as the same click. Only the UI thread is here.
func (a *trayApp) openSettingsFromClick() {
	if !a.clicks.accept(time.Now(), doubleClickWindow()) {
		logging.Log.Debug("tray: ignoring the second click of a double click")
		return
	}
	a.openSettings()
}

// doubleClickWindow is how long Windows itself waits for the second click of a
// double click.
func doubleClickWindow() time.Duration {
	ms, _, _ := procGetDoubleClickTime.Call()
	return doubleClickWindowFor(ms)
}

// openSettings serves the settings page and hands it to the browser, in a window
// of its own when that was asked for.
func (a *trayApp) openSettings() {
	open := a.settings.OpenURL
	if a.opts.AppWindow {
		open = a.settings.OpenAppWindow
	}
	url, err := open()
	if err == nil {
		return
	}
	logging.Log.Error("opening the settings page: %v", err)
	if url == "" {
		// No address to fall back to, the service itself did not start.
		messageBox(a.hwnd, i18n.L("dlg.title"), i18n.L("dlg.settingsFailOpen"), mbIconError)
		return
	}
	messageBox(a.hwnd, i18n.L("dlg.title"), i18n.Lf("dlg.startedBody", url), mbIconWarning)
}

func (a *trayApp) reloadConfig() {
	logging.Log.Info("reloading config from %s", a.state.Config().Path())
	next, err := a.state.Reload()
	if err != nil {
		logging.Log.Error("reloading config: %v", err)
		messageBox(a.hwnd, i18n.L("dlg.title"), i18n.L("err.loadFailed"), mbIconError)
		return
	}
	// The menu wording follows the newly selected language.
	a.refresh()
	logging.Log.Info("config reloaded: %s", next.Summary())
}

func (a *trayApp) exit() {
	logging.Log.Debug("removing the tray icon and destroying the tray window")
	a.settings.Stop()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&a.nid)))
	procDestroyWindow.Call(uintptr(a.hwnd))
}
