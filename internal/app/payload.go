// Package app ties the configuration, the check service and the settings page
// together, and is shared by the tray and the command line.
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
	// EntryIndex is the position in the login list of the entry the session is
	// on, so the page can mark it. It is -1 while nothing has signed in. The
	// page names the entry itself, so that the account is written in the
	// language of the interface rather than in the one the log is written in.
	EntryIndex int          `json:"entryIndex"`
	Skipped    []apiSkipped `json:"skipped"`
}

// apiSkipped is one entry of the login list that the portal refused, and how
// much of its wait is left.
type apiSkipped struct {
	Index   int    `json:"index"`
	Message string `json:"message"`
	// RetCode is the number the portal answered with, so the page can localise
	// the reason. An answer this program does not know leaves the message as the
	// portal wrote it.
	RetCode  string `json:"retCode"`
	RetryInS int    `json:"retryInS"`
}

func statusPayload(s service.Snapshot) apiStatus {
	skipped := make([]apiSkipped, 0, len(s.Skipped))
	for _, skip := range s.Skipped {
		skipped = append(skipped, apiSkipped{
			Index:    skip.Index,
			Message:  skip.Message,
			RetCode:  skip.RetCode,
			RetryInS: skip.RetryInS,
		})
	}
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
		EntryIndex:      s.EntryIndex,
		Skipped:         skipped,
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
		EntryIndex:  -1,
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
