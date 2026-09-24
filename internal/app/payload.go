// Package app ties the configuration, the check service and the settings page
// together, and is shared by the tray and the command line.
package app

import (
	"runtime"

	"sdautologin/internal/portal"
	"sdautologin/internal/service"
)

// Repo is where the project lives, shown on the About tab.
const Repo = "https://git.zqlhub.com/zqlan/SDAutoLogin"

// Version and Commit are what the release is named after, and are variables so
// the build can write into them the tree it was made from. What stands here is
// what a build that has neither falls back on.
var (
	Version = "1.2.0"
	Commit  = ""
)

// FullVersion is the version with the commit it was built from, which is what the
// command line, the log and the About tab report: two builds of one release are
// what a bug report has to tell apart.
func FullVersion() string {
	if Commit == "" {
		return Version
	}
	return Version + "+" + Commit
}

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
	EntryIndex int `json:"entryIndex"`
	// UsedISP is the service type of that entry the session is on, and
	// PreferredISP the one it should be on when it is on another one. Both are
	// suffixes, empty for the campus network itself, and the page says them in
	// the language of the interface the way it says the chips.
	UsedISP      string `json:"usedIsp"`
	PreferredISP string `json:"preferredIsp"`
	// MoveToPreferred says whether there is anything to move the session onto,
	// which is what the button that signs in again is shown for. It is a flag
	// of its own because the service type to move onto may be the campus
	// network itself, whose suffix is empty.
	MoveToPreferred bool `json:"moveToPreferred"`
	// SwitchRefused is what the portal answered when it would not sign in again
	// to move the session, or null when no such sign-in is outstanding. Its
	// presence is what tells the page the session needs a reader: the program
	// has left it as it is, and the move is now theirs to make. The ret_code
	// travels with it, so the page says the reason in its own language.
	SwitchRefused *apiRefusal  `json:"switchRefused"`
	Skipped       []apiSkipped `json:"skipped"`
}

// apiRefusal is one refusal of the portal's, as the page needs it.
type apiRefusal struct {
	Message string `json:"message"`
	RetCode string `json:"retCode"`
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
	var refused *apiRefusal
	if s.SwitchRefused != nil {
		refused = &apiRefusal{Message: s.SwitchRefused.Message, RetCode: s.SwitchRefused.RetCode}
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
		UsedISP:         s.UsedISP,
		PreferredISP:    s.PreferredISP,
		MoveToPreferred: s.MoveToPreferred,
		SwitchRefused:   refused,
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
