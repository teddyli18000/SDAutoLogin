package main

import (
	"net/url"
	"runtime"
	"strconv"
	"strings"
)

const (
	appVersion = "1.0.0"
	appRepo    = "https://git.zqlhub.com/zqlan/SDAutoLogin"
)

func platformName() string {
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

func goArch() string { return runtime.GOARCH }

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

func statusPayload(s Snapshot) apiStatus {
	return apiStatus{
		State:           stateCode(s.State),
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
func snapshotFromStatus(st Status) Snapshot {
	s := Snapshot{
		ClientIP:    st.ClientIP,
		Account:     st.Account,
		OnlineSince: st.OnlineSince,
		Message:     st.Message,
	}
	switch {
	case !st.Reachable, !st.Known:
		s.State = StateError
	case st.Online:
		s.State = StateOnline
	default:
		s.State = StateOffline
	}
	return s
}

func stateCode(s State) string {
	switch s {
	case StateOnline:
		return "online"
	case StateOffline:
		return "offline"
	case StateLoggingIn:
		return "loggingIn"
	case StateError:
		return "error"
	default:
		return "checking"
	}
}

// validateConfig returns a field name to message key map, where the keys are
// looked up in the shared string table by the page.
func validateConfig(c *Config) map[string]string {
	errs := map[string]string{}
	if strings.TrimSpace(c.Username) == "" {
		errs["username"] = "err.accountRequired"
	}
	if c.Password == "" {
		errs["password"] = "err.passwordRequired"
	}
	if c.CheckIntervalSeconds < 5 {
		errs["checkIntervalSeconds"] = "err.intervalTooSmall"
	}
	if c.HTTPTimeoutSeconds < 2 {
		errs["httpTimeoutSeconds"] = "err.timeoutTooSmall"
	}
	if !validHTTPURL(c.PortalBaseURL) {
		errs["portalBaseUrl"] = "err.urlInvalid"
	}
	if !validHTTPURL(c.EportalURL) {
		errs["eportalUrl"] = "err.urlInvalid"
	}
	for i, probe := range c.ProbeURLs {
		if strings.TrimSpace(probe) == "" {
			continue
		}
		if !validHTTPURL(probe) {
			errs["probeUrls."+strconv.Itoa(i)] = "err.probeInvalid"
		}
	}
	return errs
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// compactProbes drops blank entries and trims the rest before saving.
func compactProbes(list []string) []string {
	out := make([]string, 0, len(list))
	for _, probe := range list {
		if v := strings.TrimSpace(probe); v != "" {
			out = append(out, v)
		}
	}
	return out
}
