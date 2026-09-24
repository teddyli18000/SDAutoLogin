// Package portal is the Dr.COM eportal client: it asks the portal whether this
// client is online and submits a login when it is not.
package portal

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"sdautologin/internal/config"
	"sdautologin/internal/logging"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// Status is a snapshot of what the portal thinks about this client.
type Status struct {
	Reachable bool // the portal page answered, independent of being logged in
	// Known is true only when the online_list reply was usable, so Online can
	// be trusted. A transport error or an unexpected status leaves it false and
	// the caller must not read Online as "the session was dropped".
	Known       bool
	Online      bool // portal considers this client authenticated
	ClientIP    string
	ClientIPv6  string
	Account     string
	MAC         string
	OnlineSince string
	Message     string
}

type LoginResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Raw     string `json:"raw"`
	RetCode string `json:"retCode"`
}

// retCodeText maps the ret_code values documented in the login page script
// (error.portal_login_err in a42.js).
var retCodeText = map[string]string{
	"0":  "login accepted",
	"1":  "wrong account or password",
	"2":  "this IP is already online",
	"3":  "portal busy, try again later",
	"4":  "unknown portal error",
	"5":  "REQ_CHALLENGE failed",
	"6":  "REQ_CHALLENGE timeout",
	"7":  "authentication failed",
	"8":  "authentication timeout",
	"9":  "logout failed",
	"10": "logout timeout",
	"11": "other error",
}

// Client talks to the Dr.COM eportal through the endpoints its own pages use:
//
//	GET /                     embeds v4ip, the IP the portal sees
//	GET ?c=Portal&a=online_list   reachable from the LAN even when offline
//	GET ?c=Portal&a=login        the portal protocol, login_method=1
type Client struct {
	cfg    *config.Config
	follow *http.Client
	plain  *http.Client
}

var (
	reV4          = regexp.MustCompile(`(?i)v4ip\s*=\s*'([0-9]{1,3}(?:\.[0-9]{1,3}){3})'`)
	reV46         = regexp.MustCompile(`(?i)v46ip\s*=\s*'([0-9]{1,3}(?:\.[0-9]{1,3}){3})'`)
	reV6          = regexp.MustCompile(`(?i)v6ip\s*=\s*'([0-9a-fA-F:]{4,45})'`)
	reResult      = regexp.MustCompile(`(?i)"result"\s*:\s*"?([0-9a-zA-Z]+)"?`)
	reUserAccount = regexp.MustCompile(`"user_account"\s*:\s*"([^"]*)"`)
	reOnlineIP    = regexp.MustCompile(`"online_ip"\s*:\s*"([^"]*)"`)
	reOnlineMAC   = regexp.MustCompile(`"online_mac"\s*:\s*"([^"]*)"`)
	reOnlineTime  = regexp.MustCompile(`"online_time"\s*:\s*"([^"]*)"`)
	reOnlineV6    = regexp.MustCompile(`"online_ipv6_1"\s*:\s*"([^"]*)"`)
	reMsg         = regexp.MustCompile(`"msg"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	reRetCode     = regexp.MustCompile(`"ret_code"\s*:\s*"?(\d+)"?`)
	rePassword    = regexp.MustCompile(`(?i)(user_password=)[^&]*`)
)

func NewClient(cfg *config.Config) *Client {
	timeout := time.Duration(cfg.HTTPTimeoutSeconds) * time.Second
	return &Client{
		cfg:    cfg,
		follow: &http.Client{Timeout: timeout},
		plain: &http.Client{
			Timeout: timeout,
			// Redirects are not followed so a captive portal interception is
			// visible as a 3xx instead of silently looking like success.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// QueryStatus reads the client IP from the portal home page and then asks
// online_list whether that IP is authenticated. Only LAN hosts are contacted,
// so this still works after the session has been dropped.
func (c *Client) QueryStatus() Status {
	var st Status

	code, page, err := c.get(c.follow, c.cfg.PortalBaseURL)
	if err != nil {
		st.Message = fmt.Sprintf("portal unreachable at %s: %v", c.cfg.PortalBaseURL, err)
		return st
	}
	st.Reachable = true
	if code != http.StatusOK && code != http.StatusNoContent {
		logging.Log.Warn("portal page %s returned http %d, that reply is probably not a real portal page (body %s)",
			c.cfg.PortalBaseURL, code, Truncate(page))
	}
	st.ClientIP = firstMatch(reV4, page)
	if st.ClientIP == "" {
		st.ClientIP = firstMatch(reV46, page)
	}
	st.ClientIPv6 = firstMatch(reV6, page)
	if st.ClientIP == "" {
		// Without it the login request cannot tell the portal which client to
		// authenticate, so a reconnect would be rejected.
		logging.Log.Warn("no v4ip found on the portal page, a login request would send an empty wlan_user_ip")
	}
	logging.Log.Debug("portal page parsed: http %d, clientIp=%q ipv6=%q", code, st.ClientIP, st.ClientIPv6)

	query := c.cfg.EportalURL + "?c=Portal&a=online_list" +
		"&user_account=drcom&user_password=123" +
		"&wlan_user_mac=000000000000" +
		"&wlan_user_ip=" + url.QueryEscape(st.ClientIP) +
		"&curr_user_ip=" + url.QueryEscape(st.ClientIP) +
		"&jsVersion=" + url.QueryEscape(c.cfg.JsVersion)
	code, body, err := c.get(c.plain, query)
	if err != nil {
		st.Message = "online_list request failed: " + err.Error()
		logging.Log.Warn("online_list request failed for client ip %q: %v", st.ClientIP, err)
		return st
	}
	if code != http.StatusOK && code != http.StatusNoContent {
		st.Message = fmt.Sprintf("online_list returned http %d", code)
		logging.Log.Warn("online_list for client ip %q returned http %d, leaving the session state unknown (body %s)",
			st.ClientIP, code, Truncate(body))
		return st
	}

	result := firstMatch(reResult, body)
	if result == "" {
		st.Message = "online_list reply had no result field"
		logging.Log.Warn("online_list for client ip %q had no result field, leaving the session state unknown (body %s)",
			st.ClientIP, Truncate(body))
		return st
	}
	st.Known = true
	st.Online = result == "1" || strings.EqualFold(result, "ok")
	if ip := firstMatch(reOnlineIP, body); ip != "" {
		st.ClientIP = ip
	}
	if v6 := firstMatch(reOnlineV6, body); v6 != "" {
		st.ClientIPv6 = v6
	}
	st.Account = firstMatch(reUserAccount, body)
	st.MAC = firstMatch(reOnlineMAC, body)
	st.OnlineSince = firstMatch(reOnlineTime, body)
	logging.Log.Debug("online_list parsed: result=%q online=%v account=%q mac=%q since=%q ipv6=%q",
		result, st.Online, st.Account, st.MAC, st.OnlineSince, st.ClientIPv6)
	if st.Online {
		st.Message = "online"
	} else {
		st.Message = "not logged in"
		logging.Log.Debug("online_list raw body: %s", Truncate(body))
	}
	return st
}

// Login submits the credentials of one attempt, mirroring what the login page
// does for login_method=1. It never logs out, so a failed attempt leaves the
// current session untouched.
func (c *Client) Login(st Status, attempt config.Attempt) LoginResult {
	var result LoginResult

	mac := st.MAC
	if mac == "" {
		mac = "000000000000"
	}
	q := url.Values{}
	q.Set("c", "Portal")
	q.Set("a", "login")
	q.Set("callback", "dr"+strconv.Itoa(1000+rand.Intn(9000)))
	q.Set("login_method", "1")
	q.Set("user_account", c.cfg.Account(attempt.Username, attempt.ISP))
	q.Set("user_password", attempt.Password)
	q.Set("wlan_user_ip", st.ClientIP)
	q.Set("wlan_user_ipv6", st.ClientIPv6)
	q.Set("wlan_user_mac", mac)
	q.Set("wlan_ac_ip", "")
	q.Set("wlan_ac_name", "")
	q.Set("jsVersion", c.cfg.JsVersion)
	q.Set("v", strconv.Itoa(500+rand.Intn(9500)))

	logging.Log.Debug("login params: login_method=1 user_account=%q wlan_user_ip=%q wlan_user_ipv6=%q wlan_user_mac=%q jsVersion=%q",
		c.cfg.Account(attempt.Username, attempt.ISP), st.ClientIP, st.ClientIPv6, mac, c.cfg.JsVersion)

	code, body, err := c.get(c.plain, c.cfg.EportalURL+"?"+q.Encode())
	if err != nil {
		result.Message = "request failed: " + err.Error()
		logging.Log.Warn("login request failed: %v", err)
		return result
	}
	result.Raw = body
	res := firstMatch(reResult, body)
	result.Success = res == "1" || strings.EqualFold(res, "ok")
	result.RetCode = firstMatch(reRetCode, body)

	switch {
	case retCodeText[result.RetCode] != "":
		result.Message = retCodeText[result.RetCode]
	default:
		// The portal's own wording is echoed into the log and shown on the page
		// and the tooltip, so it goes through the same cut as the raw reply: a
		// portal that answers with a whole page must not put it all in either.
		if msg := unescapeJSON(firstMatch(reMsg, body)); msg != "" {
			result.Message = Truncate(msg)
		} else {
			result.Message = fmt.Sprintf("result=%q, HTTP %d", res, code)
		}
	}
	logging.Log.Debug("login parsed: result=%q ret_code=%q success=%v message=%q body=%s",
		res, result.RetCode, result.Success, result.Message, Truncate(body))
	return result
}

// ProbeInternet reports whether any probe host is reachable without being
// intercepted. It only feeds the status display; the login decision is based
// on the portal itself.
func (c *Client) ProbeInternet() bool {
	if len(c.cfg.ProbeURLs) == 0 {
		return true
	}
	for _, raw := range c.cfg.ProbeURLs {
		if u := strings.TrimSpace(raw); u != "" && c.probeOnce(u) {
			return true
		}
	}
	logging.Log.Debug("internet probe: no probe host answered without interception")
	return false
}

func (c *Client) probeOnce(u string) bool {
	code, body, err := c.get(c.plain, u)
	if err != nil {
		logging.Log.Debug("internet probe %s: unreachable (%v)", u, err)
		return false
	}

	ok := false
	reason := ""
	switch {
	case code == http.StatusNoContent:
		ok, reason = true, "expected status"
	case code != http.StatusOK:
		reason = fmt.Sprintf("unexpected status %d", code)
	default:
		lower := strings.ToLower(u)
		switch {
		case strings.Contains(lower, "generate_204"):
			reason = "expected 204 but got 200, intercepted"
		case strings.Contains(lower, "connecttest.txt"):
			ok = strings.Contains(body, "Connect Test")
			reason = markerReason(ok)
		case strings.Contains(lower, "baidu.com"):
			ok = strings.Contains(strings.ToLower(body), "baidu")
			reason = markerReason(ok)
		default:
			ok = !looksLikePortal(body)
			if ok {
				reason = "no portal markers"
			} else {
				reason = "portal markers present"
			}
		}
	}
	logging.Log.Debug("internet probe %s: http %d (%s) -> reachable=%v", u, code, reason, ok)
	return ok
}

func markerReason(ok bool) string {
	if ok {
		return "marker found"
	}
	return "marker missing, intercepted"
}

func looksLikePortal(body string) bool {
	return strings.Contains(body, "Dr.COM") || strings.Contains(body, "eportal") ||
		strings.Contains(body, "DDDDD") || strings.Contains(body, "wlan_user_ip")
}

func (c *Client) get(client *http.Client, rawURL string) (int, string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		logging.Log.Debug("http get %s: bad request: %v", c.safeURL(rawURL), err)
		return 0, "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		logging.Log.Debug("http get %s failed after %d ms: %v", c.safeURL(rawURL), elapsedMS(start), err)
		return 0, "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		logging.Log.Debug("http get %s: http %d but reading body failed: %v", c.safeURL(rawURL), resp.StatusCode, err)
		return resp.StatusCode, "", err
	}
	logging.Log.Debug("http get %s -> http %d, %d bytes in %d ms",
		c.safeURL(rawURL), resp.StatusCode, len(data), elapsedMS(start))
	return resp.StatusCode, string(data), nil
}

// safeURL removes the password from a request URL before it is logged.
func (c *Client) safeURL(rawURL string) string {
	return MaskSecrets(rawURL)
}

// MaskSecrets removes the password from a string before it is logged, so a
// caller that prints the portal's own reply gets the same protection as a caller
// that logs a request URL. It masks the parameter rather than a particular
// password, since the login list may hold several accounts; user_account is a
// login name and is deliberately left readable.
func MaskSecrets(s string) string {
	return rePassword.ReplaceAllString(s, "${1}***")
}

func elapsedMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func firstMatch(re *regexp.Regexp, s string) string {
	if s == "" {
		return ""
	}
	if m := re.FindStringSubmatch(s); len(m) >= 2 {
		return m[1]
	}
	return ""
}

// unescapeJSON resolves the \uXXXX escapes the portal uses for Chinese text.
func unescapeJSON(s string) string {
	if s == "" {
		return ""
	}
	if v, err := strconv.Unquote(`"` + s + `"`); err == nil {
		return v
	}
	return s
}

// truncateLimit is how much of a portal reply is kept. It is a byte count, so a
// line of Chinese text is cut shorter than a line of Latin text.
const truncateLimit = 300

// Truncate collapses whitespace and cuts s to a length that keeps a log line
// readable. Portal replies are echoed through it, so the whole body never
// floods the log.
func Truncate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= truncateLimit {
		return s
	}
	// The cut is moved back to a character boundary: cutting a multi-byte
	// character in half would write a broken sequence into the log, and log
	// lines are read as text by whatever tail tool the user has.
	cut := truncateLimit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
