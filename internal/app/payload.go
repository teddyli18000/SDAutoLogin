// Package app ties the configuration, the check service and the settings page
// together, and is what the tray and the command line share.
package app

import (
	"runtime"

	"sdautologin/internal/portal"
	"sdautologin/internal/service"
)

// Version and Repo identify the release and are shown on the About tab.
const (
	Version = "1.0.0"
	Repo    = "https://git.zqlhub.com/zqlan/SDAutoLogin"
)

// PlatformName is the operating system in the spelling the interface uses.
func PlatformName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}

// Arch is the build architecture, shown next to the platform.
func Arch() string { return runtime.GOARCH }

// apiStatus is the JSON shape of a status snapshot. The state travels as a
// stable code so the page can localise it.
type apiStatus struct {
	State           string `json:"state"`
	ClientIP        string `json:"clientIp"`
	Account         string `json:"account"`
	OnlineSince     string `json:"onlineSince"`
	Message         string `json:"message"`
	LastLoginAt     string `json:"lastLoginAt"`
	LastError       string `json:"lastError"`
	InternetChecked bool   `json:"internetChecked"`
	InternetOK      bool   `json:"internetOk"`
}

func statusPayload(s service.Snapshot) apiStatus {
	return apiStatus{
		State:           s.State.Code(),
		ClientIP:        s.ClientIP,
		Account:         s.Account,
		OnlineSince:     s.OnlineSince,
		Message:         s.Message,
		LastLoginAt:     s.LastLoginAt,
		LastError:       s.LastError,
		InternetChecked: s.InternetChecked,
		InternetOK:      s.InternetOK,
	}
}

// snapshotFromStatus adapts a portal reply to the status shape the page
// renders, so the login self test can show the state before and after.
func snapshotFromStatus(st portal.Status) service.Snapshot {
	s := service.Snapshot{
		ClientIP:    st.ClientIP,
		Account:     st.Account,
		OnlineSince: st.OnlineSince,
		Message:     st.Message,
	}
	switch {
	case !st.Reachable, !st.Known:
		s.State = service.StateError
	case st.Online:
		s.State = service.StateOnline
	default:
		s.State = service.StateOffline
	}
	return s
}
