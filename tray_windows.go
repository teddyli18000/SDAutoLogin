//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// Message ids posted to the hidden tray window.
const (
	wmTrayIcon    = wmApp + 1
	wmTrayRefresh = wmApp + 2
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
	svc    *Service
	cfg    *Config
	appDir string

	hwnd syscall.Handle
	nid  notifyIconData

	mu   sync.Mutex
	snap Snapshot

	iconApp  syscall.Handle
	iconInfo syscall.Handle
	iconWarn syscall.Handle
	iconErr  syscall.Handle

	settings *settingsWindow

	// lastState is only touched by the UI thread, see refresh.
	lastState State
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
		case wmLButtonDblClk:
			Log.Debug("tray: double click, opening settings")
			trayInstance.showSettings()
		case wmRButtonUp, wmLButtonUp:
			Log.Debug("tray: menu requested (mouse message %#x)", mouse)
			trayInstance.showMenu()
		}
		return 0
	case wmTrayRefresh:
		if trayInstance != nil {
			trayInstance.refresh()
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

func runUI(svc *Service, cfg *Config, appDir string) {
	// The tray window and its message loop must stay on one OS thread.
	runtime.LockOSThread()

	app := &trayApp{svc: svc, cfg: cfg, appDir: appDir}
	trayInstance = app

	if err := app.create(); err != nil {
		Log.Error("creating tray icon: %v, falling back to background mode", err)
		runHeadless(svc, cfg, appDir)
		return
	}

	svc.SetListener(app.onStatus)
	svc.Start()
	app.refresh()

	if !cfg.Configured() {
		app.showSettings()
	}

	Log.Debug("entering the tray message loop")
	app.messageLoop()
	svc.Stop()
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

	a.iconApp = loadSystemIcon(idiApplication)
	a.iconInfo = loadSystemIcon(idiInformation)
	a.iconWarn = loadSystemIcon(idiWarning)
	a.iconErr = loadSystemIcon(idiError)

	a.nid = notifyIconData{
		HWnd:             a.hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: wmTrayIcon,
		HIcon:            a.iconApp,
	}
	a.nid.CbSize = uint32(unsafe.Sizeof(a.nid))
	putUTF16(a.nid.SzTip[:], appTitle+" - checking")
	if r, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&a.nid))); r == 0 {
		return fmt.Errorf("Shell_NotifyIcon: %v", err)
	}
	Log.Info("tray icon created")
	Log.Debug("tray window hwnd=%#x, notify icon id=%d, icons app=%#x info=%#x warn=%#x error=%#x",
		uintptr(a.hwnd), a.nid.UID, uintptr(a.iconApp), uintptr(a.iconInfo), uintptr(a.iconWarn), uintptr(a.iconErr))
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

// onStatus is called from the worker goroutine. It only stores the snapshot and
// wakes the UI thread.
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
	if s.State != a.lastState {
		a.lastState = s.State
		Log.Debug("tray icon updated to %s (internet checked=%v ok=%v)",
			s.State, s.InternetChecked, s.InternetOK)
	}
	a.nid.HIcon = a.iconFor(s.State)
	a.nid.UFlags = nifMessage | nifIcon | nifTip
	putUTF16(a.nid.SzTip[:], a.tooltip(s))
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&a.nid)))
}

func (a *trayApp) iconFor(st State) syscall.Handle {
	switch st {
	case StateOnline:
		return a.iconInfo
	case StateLoggingIn, StateOffline:
		return a.iconWarn
	case StateError:
		return a.iconErr
	default:
		return a.iconApp
	}
}

func (a *trayApp) tooltip(s Snapshot) string {
	out := appTitle + "\nStatus: " + describeSnapshot(s)
	if s.InternetChecked {
		if s.InternetOK {
			out += "\nInternet: reachable"
		} else {
			out += "\nInternet: unreachable"
		}
	}
	return out
}

func (a *trayApp) showMenu() {
	s := a.Snapshot()
	Log.Debug("building tray menu for status %s", describeSnapshot(s))
	menu, _, _ := procCreatePopupMenu.Call()

	status := "Status: " + s.State.String()
	if s.ClientIP != "" {
		status += " " + s.ClientIP
	}
	appendMenu(menu, mfString|mfGrayed, 0, status)
	if s.LastLoginAt != "" {
		appendMenu(menu, mfString|mfGrayed, 0, "Last login: "+s.LastLoginAt)
	}
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, cmdCheckNow, "Check and log in now")
	appendMenu(menu, mfString, cmdSettings, "Settings...")
	appendMenu(menu, mfSeparator, 0, "")
	if autostartEnabled() {
		appendMenu(menu, mfString|mfChecked, cmdAutostart, "Run at startup")
	} else {
		appendMenu(menu, mfString, cmdAutostart, "Run at startup")
	}
	appendMenu(menu, mfString, cmdReload, "Reload config")
	appendMenu(menu, mfString, cmdOpenCfg, "Open config file")
	appendMenu(menu, mfString, cmdOpenLog, "Open log")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, cmdExit, "Exit")

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
	Log.Debug("tray command selected: id=%d (%s)", cmd, commandName(cmd))
	switch cmd {
	case cmdCheckNow:
		Log.Info("manual check requested from the tray menu")
		a.svc.Trigger()
	case cmdSettings:
		a.showSettings()
	case cmdReload:
		a.reloadConfig()
	case cmdOpenCfg:
		Log.Info("opening config file %s", a.cfg.Path())
		openWithShell(a.cfg.Path())
	case cmdOpenLog:
		if p := Log.Path(); p != "" {
			Log.Info("opening log file %s", p)
			openWithShell(p)
		}
	case cmdAutostart:
		on := !autostartEnabled()
		if setAutostart(on) {
			Log.Info("autostart set to %v", on)
		} else {
			messageBox(a.hwnd, appTitle, "Could not update the startup entry, see the log.", mbIconWarning)
		}
	case cmdExit:
		Log.Info("exit requested from the tray menu")
		a.exit()
	}
}

func commandName(cmd uint32) string {
	switch cmd {
	case cmdCheckNow:
		return "check now"
	case cmdSettings:
		return "settings"
	case cmdReload:
		return "reload config"
	case cmdOpenCfg:
		return "open config file"
	case cmdAutostart:
		return "toggle autostart"
	case cmdOpenLog:
		return "open log"
	case cmdExit:
		return "exit"
	default:
		return "unknown"
	}
}

func (a *trayApp) reloadConfig() {
	Log.Info("reloading config from %s", a.cfg.Path())
	cfg, err := LoadConfig(a.cfg.Path())
	if err != nil {
		Log.Error("reloading config: %v", err)
		messageBox(a.hwnd, appTitle, "Could not reload the config: "+err.Error(), mbIconError)
		return
	}
	Log.SetVerbose(cfg.VerboseLog)
	a.cfg = cfg
	a.svc.Restart(cfg)
	a.svc.Trigger()
	Log.Info("config reloaded: %s", cfg.Summary())
}

func (a *trayApp) showSettings() {
	if a.settings == nil {
		Log.Debug("creating the settings window")
		w := newSettingsWindow(a)
		if err := w.create(); err != nil {
			Log.Error("creating settings window: %v", err)
			messageBox(a.hwnd, appTitle, "Could not open the settings window: "+err.Error(), mbIconError)
			return
		}
		a.settings = w
	}
	a.settings.show()
}

func (a *trayApp) exit() {
	Log.Debug("removing the tray icon and destroying the tray window")
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&a.nid)))
	procDestroyWindow.Call(uintptr(a.hwnd))
}
