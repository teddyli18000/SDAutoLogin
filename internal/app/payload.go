// Package app ties the configuration, the check service and the settings page
// together, and is shared by the tray and the command line.
package app

import (
	"net/url"
	"runtime"
	"strings"

	"sdautologin/internal/portal"
	"sdautologin/internal/service"
	"sdautologin/internal/update"
)

// Repo is where the project lives, shown on the About tab.
const Repo = "https://git.zqlhub.com/zqlan/SDAutoLogin"

// UpdateClient is a client for the release page of this project. The address is
// Repo above, split into the instance and the repository it names, which is the
// pair the forge API is addressed with.
func UpdateClient() *update.Client {
	instance, repo := forgeAddress()
	return update.NewClient(instance, repo)
}

// forgeAddress splits Repo into those two halves.
func forgeAddress() (instance, repo string) {
	u, err := url.Parse(Repo)
	if err != nil || u.Host == "" {
		// Repo is a constant, so this is a program that was edited wrongly. The
		// address goes back whole and the request fails where it is made.
		return Repo, ""
	}
	return u.Scheme + "://" + u.Host, strings.Trim(u.Path, "/")
}

// Version and Commit are what the release is named after, and are variables so
// the build can write into them the tree it was made from. What stands here is
// what a build that has neither falls back on.
var (
	Version = "1.3.0-alpha.2"
	Commit  = ""
)

// FullVersion is the version with the commit it was built from. The command line,
// the log and the About tab report it, since two builds of one release have to be
// told apart in a bug report.
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
	// CheckedAt is when the round that produced the rest of this read the portal,
	// as RFC 3339 in UTC. The page polls faster than the loop runs, so it needs
	// this to say how old the reading is.
	CheckedAt string `json:"checkedAt"`
	// EntryIndex is the position in the login list of the entry the session is
	// on, so the page can mark it. It is -1 while nothing has signed in. The page
	// names the entry itself, in the language of the interface.
	EntryIndex int `json:"entryIndex"`
	// UsedISP is the service type of that entry the session is on; PreferredISP
	// the one it should be on when it is on another. Both are suffixes, empty for
	// the campus network itself, and the page names them in the interface language.
	UsedISP      string `json:"usedIsp"`
	PreferredISP string `json:"preferredIsp"`
	// MoveToPreferred says whether there is anything to move the session onto, and
	// the button that signs in again is shown on it. It is a flag of its own since
	// the service type to move onto may be the campus network (empty suffix).
	MoveToPreferred bool `json:"moveToPreferred"`
	// SwitchRefused is what the portal answered when it would not sign in again to
	// move the session, or null when no such sign-in is outstanding. Its presence
	// tells the page the session needs a reader, and its ret_code gives the reason.
	SwitchRefused *apiRefusal  `json:"switchRefused"`
	Skipped       []apiSkipped `json:"skipped"`
	// Update is the last look for a newer release, or null while the check is off
	// or has not run yet. It travels whether or not the check is on, so the page
	// can show what a manual look found.
	Update *apiUpdate `json:"update"`
}

// apiUpdate is the last look for a newer release, as the page shows it. The
// fields are the ones the update command prints, since they answer the same
// question about the same copy.
type apiUpdate struct {
	// CheckedAt is when the forge was asked, as RFC 3339 in UTC, so the page can
	// say how old the answer is the same way it does for the session.
	CheckedAt string `json:"checkedAt"`
	// Message is the one line to show a reader: what the look found, or what was
	// installed.
	Message string `json:"message"`
	Current string `json:"current"`
	// Latest is the release that was found, empty when nothing is newer.
	Latest  string `json:"latest"`
	Channel string `json:"channel"`
	Newer   bool   `json:"newer"`
	// Signed is whether this build can check what it downloads, and Installable
	// whether this copy could put the release in place. Reason says why not.
	Signed      bool   `json:"signed"`
	Installable bool   `json:"installable"`
	Reason      string `json:"reason,omitempty"`
	// Installed is the program file a release was written over, empty when nothing
	// was installed.
	Installed string `json:"installed,omitempty"`
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
	var release *apiUpdate
	if s.Update != nil {
		release = &apiUpdate{
			CheckedAt:   s.Update.CheckedAt,
			Message:     s.Update.Message,
			Current:     s.Update.Current,
			Latest:      s.Update.Latest,
			Channel:     s.Update.Channel,
			Newer:       s.Update.Newer,
			Signed:      s.Update.Signed,
			Installable: s.Update.Installable,
			Reason:      s.Update.Reason,
			Installed:   s.Update.Installed,
		}
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
		CheckedAt:       s.CheckedAt,
		EntryIndex:      s.EntryIndex,
		UsedISP:         s.UsedISP,
		PreferredISP:    s.PreferredISP,
		MoveToPreferred: s.MoveToPreferred,
		SwitchRefused:   refused,
		Skipped:         skipped,
		Update:          release,
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
