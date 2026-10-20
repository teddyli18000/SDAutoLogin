// Package i18n holds every user-visible string and the language selection. The
// settings page is served the same table as JSON, so the tray and the page
// cannot disagree.
package i18n

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Lang is the language of the user interface. Log lines and code comments stay
// English so pasted logs stay readable.
type Lang string

const (
	LangAuto Lang = "auto"
	LangZH   Lang = "zh"
	LangEN   Lang = "en"
)

// text holds one user-visible string in every supported language.
type text struct {
	zh string
	en string
}

// uiText holds every interface string. Placeholders use {0}, {1} and are
// substituted by both Go and JavaScript.
var uiText = map[string]text{
	"app.name": {"校园网自动登录", "Campus Auto Login"},

	"state.checking":  {"检测中", "Checking"},
	"state.online":    {"已在线", "Online"},
	"state.offline":   {"未登录", "Offline"},
	"state.loggingIn": {"正在登录", "Signing in"},
	"state.error":     {"异常", "Error"},
	"state.unknown":   {"状态未知", "State unknown"},

	"tray.status":          {"状态：{0}", "Status: {0}"},
	"tray.updateAvailable": {"有新版本 {0}", "Version {0} is available"},
	"tray.internetOk":      {"外网：可达", "Internet: reachable"},
	"tray.internetFail":    {"外网：不通", "Internet: unreachable"},
	"tray.clientIP":        {"客户端 IP：{0}", "Client IP: {0}"},
	"tray.lastLogin":       {"上次登录：{0}", "Last login: {0}"},
	"tray.checkNow":        {"立即检测并登录", "Check and log in now"},
	"tray.settings":        {"设置…", "Settings..."},
	"tray.autostart":       {"开机自动运行", "Run at startup"},
	"tray.openConfig":      {"打开配置文件", "Open config file"},
	"tray.openLog":         {"打开日志", "Open log"},
	"tray.reload":          {"重新加载配置", "Reload config"},
	"tray.exit":            {"退出", "Exit"},
	"tray.checkRequested":  {"已触发一次检测", "A check has been requested"},
	// Shown when the portal would not sign the session in again to move it onto
	// the service type its entry puts first: the session is up, so there is
	// nothing broken, but the move is left for the reader to make.
	"tray.switchRefused": {"未能切换到首选服务类型，请在设置页处理", "Could not move onto the preferred service type, see the settings"},

	"balloon.loggedIn":        {"已自动重新登录", "Signed back in"},
	"balloon.reconnectFailed": {"自动登录失败，请检查设置", "Auto login failed, check the settings"},
	"balloon.switchRefused": {"会话在线，但未能切换到首选服务类型，请在设置页处理",
		"Signed in, but the move onto the preferred service type was refused - see the settings"},

	"dlg.title":            {"校园网自动登录", "Campus Auto Login"},
	"dlg.autostartFailed":  {"设置开机自启失败，请查看日志。", "Could not update the startup entry, see the log."},
	"dlg.settingsFailOpen": {"无法启动设置服务，请查看日志。", "Could not start the settings service, see the log."},
	"dlg.startedBody":      {"如果浏览器没有自动打开，请访问：{0}", "If the browser did not open, visit: {0}"},
}

func init() {
	for k, v := range webText {
		uiText[k] = v
	}
}

var (
	langMu      sync.RWMutex
	langSetting = LangAuto
	langCurrent = LangEN
)

// SetLang applies a language setting; LangAuto resolves against the OS.
func SetLang(l Lang) {
	resolved := l
	switch l {
	case LangZH:
		resolved = LangZH
	case LangEN:
		resolved = LangEN
	default:
		resolved = detectSystemLang()
	}
	langMu.Lock()
	langSetting = l
	langCurrent = resolved
	langMu.Unlock()
}

func LangSetting() Lang {
	langMu.RLock()
	defer langMu.RUnlock()
	return langSetting
}

func CurrentLang() Lang {
	langMu.RLock()
	defer langMu.RUnlock()
	return langCurrent
}

// L returns the string for key in the active language, or key itself when the
// table holds no entry.
func L(key string) string {
	t, ok := uiText[key]
	if !ok {
		return key
	}
	if CurrentLang() == LangZH && t.zh != "" {
		return t.zh
	}
	if t.en != "" {
		return t.en
	}
	return key
}

// Lf substitutes {0}, {1}, ... in the string for key.
func Lf(key string, args ...any) string {
	out := L(key)
	for i, a := range args {
		out = strings.ReplaceAll(out, "{"+strconv.Itoa(i)+"}", fmt.Sprint(a))
	}
	return out
}

// ALL returns every message in the given language, keyed by identifier. The
// settings page consumes this so both layers share one wording source.
func ALL(l Lang) map[string]string {
	pick := func(t text) string {
		if l == LangZH && t.zh != "" {
			return t.zh
		}
		return t.en
	}
	out := make(map[string]string, len(uiText))
	for k, v := range uiText {
		out[k] = pick(v)
	}
	return out
}
