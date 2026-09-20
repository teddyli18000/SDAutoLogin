//go:build windows

package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// Control ids.
const (
	idUsername     = 2001
	idPassword     = 2002
	idOperator     = 2003
	idRemember     = 2004
	idAutoLogin    = 2005
	idInterval     = 2006
	idPortal       = 2007
	idEportal      = 2008
	idPrefix       = 2009
	idSuffix       = 2010
	idStatus       = 2011
	idCheckInternt = 2012
	idVerbose      = 2013
	idSave         = 2020
	idCancel       = 2021
)

const (
	cbsDropDownList = 0x0003
	wsVScroll       = 0x00200000
	cbAddString     = 0x0143
	cbSetCurSel     = 0x014E
	cbGetCurSel     = 0x0147
	cbnSelChange    = 1
	cwUseDefault    = 0x80000000
)

const (
	clientW = 470
	clientH = 440
)

type settingsWindow struct {
	app *trayApp

	hwnd     syscall.Handle
	centered bool

	hUser         syscall.Handle
	hPass         syscall.Handle
	hOperator     syscall.Handle
	hRemember     syscall.Handle
	hAutoLogin    syscall.Handle
	hCheckInternt syscall.Handle
	hVerbose      syscall.Handle
	hInterval     syscall.Handle
	hPortal       syscall.Handle
	hEportal      syscall.Handle
	hPrefix       syscall.Handle
	hSuffix       syscall.Handle
	hStatus       syscall.Handle

	visible bool
}

var settingsInstance *settingsWindow

var settingsWndProc = syscall.NewCallback(settingsProc)

func settingsProc(hwnd, umsg, wparam, lparam uintptr) uintptr {
	sw := settingsInstance
	switch uint32(umsg) {
	case wmCommand:
		if sw != nil {
			id := uint32(wparam) & 0xffff
			code := uint32(wparam) >> 16
			switch {
			case id == idSave && code == bnClicked:
				sw.onSave()
			case id == idCancel && code == bnClicked:
				Log.Debug("settings: cancel clicked, discarding the form")
				sw.hide()
			case id == idOperator && code == cbnSelChange:
				sw.onOperatorChanged()
			}
		}
		return 0
	case wmClose:
		// Hiding instead of destroying keeps the window reusable.
		if sw != nil {
			Log.Debug("settings: close requested, hiding the window")
			sw.hide()
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, umsg, wparam, lparam)
	return r
}

func newSettingsWindow(app *trayApp) *settingsWindow {
	w := &settingsWindow{app: app}
	settingsInstance = w
	return w
}

func (w *settingsWindow) create() error {
	const className = "SDAutoLoginSettingsWnd"
	wc := wndClassExW{
		LpfnWndProc:   settingsWndProc,
		HInstance:     syscall.Handle(moduleHandle()),
		LpszClassName: u16ptr(className),
		HCursor:       loadSystemCursor(),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassExW: %v", err)
	}

	style := uint32(wsCaption | wsSysMenu | wsMinimizeBox)
	rc := rect{Right: clientW, Bottom: clientH}
	procAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&rc)), uintptr(style), 0, 0)

	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(u16ptr(className))),
		uintptr(unsafe.Pointer(u16ptr(appTitle+" - Settings"))),
		uintptr(style),
		uintptr(cwUseDefault), uintptr(cwUseDefault),
		uintptr(rc.Right-rc.Left), uintptr(rc.Bottom-rc.Top),
		0, 0, moduleHandle(), 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %v", err)
	}
	w.hwnd = syscall.Handle(hwnd)
	w.buildControls()
	return nil
}

func (w *settingsWindow) buildControls() {
	font := guiFont()

	mk := func(class, text string, style uint32, x, y, cw, ch int32, id uintptr) syscall.Handle {
		h := createControl(w.hwnd, class, text, style, x, y, cw, ch, id)
		if h != 0 {
			sendMessage(h, wmSetFont, font, 1)
		}
		return h
	}
	label := func(text string, x, y, cw, ch int32) syscall.Handle {
		return mk("STATIC", text, wsChild|wsVisible, x, y, cw, ch, 0)
	}
	edit := func(text string, x, y, cw, ch int32, id uintptr, extra uint32) syscall.Handle {
		return mk("EDIT", text, wsChild|wsVisible|wsBorder|wsTabStop|esAutoHScroll|extra, x, y, cw, ch, id)
	}
	check := func(text string, x, y, cw, ch int32, id uintptr) syscall.Handle {
		return mk("BUTTON", text, wsChild|wsVisible|wsTabStop|bsAutoCheckBox, x, y, cw, ch, id)
	}

	const lx, lw = 20, 100
	const ix, iw = 125, 325

	label("Account", lx, 21, lw, 22)
	w.hUser = edit("", ix, 18, iw, 24, idUsername, 0)

	label("Password", lx, 53, lw, 22)
	w.hPass = edit("", ix, 50, iw, 24, idPassword, esPassword)

	label("ISP", lx, 85, lw, 22)
	w.hOperator = mk("COMBOBOX", "",
		wsChild|wsVisible|wsTabStop|wsVScroll|cbsDropDownList, ix, 85, iw, 200, idOperator)
	for _, op := range Operators {
		sendMessage(w.hOperator, cbAddString, 0, uintptr(unsafe.Pointer(u16ptr(op.Name))))
	}
	sendMessage(w.hOperator, cbSetCurSel, 0, 0)

	w.hRemember = check("Remember password (write to config file)", ix, 114, iw, 22, idRemember)
	w.hAutoLogin = check("Auto login after disconnection", ix, 138, iw, 22, idAutoLogin)
	w.hCheckInternt = check("Probe Internet reachability (status only)", ix, 162, iw, 22, idCheckInternt)
	w.hVerbose = check("Verbose logging (per-check diagnostics)", ix, 186, iw, 22, idVerbose)

	label("Interval (s)", lx, 217, lw, 22)
	w.hInterval = edit("15", ix, 214, 80, 24, idInterval, 0)

	label("Portal home", lx, 249, lw, 22)
	w.hPortal = edit("", ix, 246, iw, 24, idPortal, 0)

	label("API endpoint", lx, 281, lw, 22)
	w.hEportal = edit("", ix, 278, iw, 24, idEportal, 0)

	label("Prefix/suffix", lx, 313, lw, 22)
	w.hPrefix = edit("", ix, 310, 90, 24, idPrefix, 0)
	label("suffix", 222, 313, 45, 22)
	w.hSuffix = edit("", 270, 310, 180, 24, idSuffix, 0)

	w.hStatus = mk("STATIC", "", wsChild|wsVisible, lx, 342, clientW-40, 50, idStatus)

	mk("BUTTON", "Save", wsChild|wsVisible|wsTabStop, 280, 400, 80, 28, idSave)
	mk("BUTTON", "Cancel", wsChild|wsVisible|wsTabStop, 370, 400, 80, 28, idCancel)

	Log.Debug("settings window created (hwnd=%#x): account, password, isp, 4 checkboxes, interval, portal, api, prefix, suffix, status",
		uintptr(w.hwnd))
}

func (w *settingsWindow) show() {
	w.loadFromConfig()
	setWindowText(w.hStatus, w.statusText())
	procShowWindow.Call(uintptr(w.hwnd), swShowNormal)
	if !w.centered {
		centerWindow(uintptr(w.hwnd))
		w.centered = true
	}
	procSetForegroundWindow.Call(uintptr(w.hwnd))
	procSetFocus.Call(uintptr(w.hUser))
	w.visible = true
	Log.Debug("settings window shown")
}

func (w *settingsWindow) hide() {
	procShowWindow.Call(uintptr(w.hwnd), swHide)
	if w.visible {
		w.visible = false
		Log.Debug("settings window hidden")
	}
}

func (w *settingsWindow) loadFromConfig() {
	cfg := w.app.cfg
	setWindowText(w.hUser, cfg.Username)
	setWindowText(w.hPass, cfg.Password)

	sel := uintptr(0)
	for i, op := range Operators {
		if op.Suffix == cfg.AccountSuffix {
			sel = uintptr(i)
			break
		}
	}
	sendMessage(w.hOperator, cbSetCurSel, sel, 0)

	setChecked(w.hRemember, cfg.RememberPassword)
	setChecked(w.hAutoLogin, cfg.AutoLogin)
	setChecked(w.hCheckInternt, cfg.CheckInternet)
	setChecked(w.hVerbose, cfg.VerboseLog)
	setWindowText(w.hInterval, strconv.Itoa(cfg.CheckIntervalSeconds))
	setWindowText(w.hPortal, cfg.PortalBaseURL)
	setWindowText(w.hEportal, cfg.EportalURL)
	setWindowText(w.hPrefix, cfg.AccountPrefix)
	setWindowText(w.hSuffix, cfg.AccountSuffix)
}

// onOperatorChanged mirrors the dropdown into the suffix field, which stays
// editable so a custom suffix can still be typed in.
func (w *settingsWindow) onOperatorChanged() {
	idx := int(sendMessage(w.hOperator, cbGetCurSel, 0, 0))
	if idx >= 0 && idx < len(Operators) {
		Log.Debug("settings: isp changed to %q, suffix set to %q", Operators[idx].Name, Operators[idx].Suffix)
		setWindowText(w.hSuffix, Operators[idx].Suffix)
	}
}

func (w *settingsWindow) onSave() {
	next := *w.app.cfg

	username := strings.TrimSpace(getWindowText(w.hUser))
	if username == "" {
		Log.Warn("settings: refusing to save an empty account")
		messageBox(w.hwnd, appTitle, "Account must not be empty.", mbIconWarning)
		return
	}
	next.Username = username
	next.Password = getWindowText(w.hPass)
	next.RememberPassword = isChecked(w.hRemember)
	next.AutoLogin = isChecked(w.hAutoLogin)
	next.CheckInternet = isChecked(w.hCheckInternt)
	next.VerboseLog = isChecked(w.hVerbose)

	if interval, err := strconv.Atoi(strings.TrimSpace(getWindowText(w.hInterval))); err == nil && interval >= 5 {
		next.CheckIntervalSeconds = interval
	}

	next.AccountPrefix = getWindowText(w.hPrefix)
	next.AccountSuffix = strings.TrimSpace(getWindowText(w.hSuffix))

	if v := strings.TrimSpace(getWindowText(w.hPortal)); v != "" {
		next.PortalBaseURL = v
	}
	if v := strings.TrimSpace(getWindowText(w.hEportal)); v != "" {
		next.EportalURL = v
	}

	if err := next.Save(); err != nil {
		Log.Error("saving config: %v", err)
		messageBox(w.hwnd, appTitle, "Could not save the config: "+err.Error(), mbIconError)
		return
	}

	// Verbose output may just have been switched on, so do this before logging.
	Log.SetVerbose(next.VerboseLog)
	w.app.cfg = &next
	w.app.svc.Restart(&next)
	w.app.svc.Trigger()
	setWindowText(w.hStatus, w.statusText())
	Log.Info("settings saved, resubmitting one check: %s", next.Summary())
	w.hide()
}

func (w *settingsWindow) statusText() string {
	s := w.app.Snapshot()
	lines := []string{
		"Status: " + describeSnapshot(s),
		"Config: " + w.app.cfg.Path(),
	}
	if s.LastError != "" {
		lines = append(lines, "Last error: "+s.LastError)
	}
	return strings.Join(lines, "\n")
}

func setChecked(h syscall.Handle, on bool) {
	v := uintptr(0)
	if on {
		v = bstChecked
	}
	sendMessage(h, bmSetCheck, v, 0)
}

func isChecked(h syscall.Handle) bool {
	return sendMessage(h, bmGetCheck, 0, 0) == bstChecked
}
