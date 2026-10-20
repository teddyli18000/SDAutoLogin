// Package portal is the Dr.COM eportal client: it asks the portal whether this
// client is online and submits a login when it is not.
package portal

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
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

const (
	// replyLimit is how much of a reply is read. The portal answers a small JSON
	// object, so a reply larger than this is not one this program can use; the
	// byte past the limit is what tells a complete reply from a cut one.
	replyLimit = 4 << 20
	// homeRedirectLimit is how many redirects the portal home page may take. A
	// portal moves a browser on to a page of its own, so a hop or two is normal;
	// a chain longer than this is not, and each hop is a request the
	// configuration never named.
	homeRedirectLimit = 3
	// defaultPortalHost is the campus portal the shipped configuration points at
	// and the address a user recognises. Any other one is worth saying out loud,
	// because every credential the program holds is sent to it.
	defaultPortalHost = "10.9.1.3"
	// minMaskedSecretLen is the shortest password masked by its value. A one or
	// two character value occurs throughout a sentence, and replacing it would
	// leave the log unreadable; MaskSecrets still covers such a password
	// wherever it is spelled as the user_password parameter.
	minMaskedSecretLen = 3
)

// errReplyTooLarge marks a reply that was not read in full. A truncated reply is
// refused rather than parsed: a field placed past the cut would look like a
// field that was never there, which is how a forged answer turns into a report
// of a successful login.
var errReplyTooLarge = errors.New("the reply was larger than the read limit")

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

// RetCodeText is what the portal's own script calls this ret_code, and whether
// the code is one of the twelve it documents. The caller walks the login list
// on what an answer blames, and only two of the twelve blame the account, so an
// answer outside them has to be named and not read as either kind.
func RetCodeText(code string) (string, bool) {
	text, ok := retCodeText[code]
	return text, ok
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
	// rePassword matches the password in every spelling a reply can echo it in:
	// the query string of the request, and the JSON a proxy repeats in an error
	// page. An escaped pair counts as part of the value, so a quote in it does not
	// cut the mask.
	rePassword = regexp.MustCompile(`(?i)(user_password["']?\s*[:=]\s*["']?)(?:[^"'&\s<>\\]|\\.)*`)
)

func NewClient(cfg *config.Config) *Client {
	timeout := time.Duration(cfg.HTTPTimeoutSeconds) * time.Second
	transport := portalTransport()
	c := &Client{
		cfg: cfg,
		follow: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			// A page that sends the browser somewhere else is the shape an
			// interception has, and following it makes the client fetch a host the
			// configuration never named. The address read off whichever page
			// answers is then used for the login, so the fetch stays on the host
			// it started from and takes no more than a few hops.
			CheckRedirect: checkHomeRedirect,
		},
		plain: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			// Redirects are not followed so a captive portal interception is
			// visible as a 3xx instead of silently looking like success. On the
			// login that also keeps the password from being carried to the host a
			// redirect names.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	c.warnAboutTheTransport()
	return c
}

// portalTransport is the transport every request goes through: the default one
// without its proxy. The login is a GET with the password in its URL, so an
// HTTP_PROXY in the environment would hand the credentials to whatever it names,
// and a campus portal is reached on the LAN rather than through one. There is no
// proxy setting in this program, so the environment is not consulted at all.
func portalTransport() *http.Transport {
	def, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	out := def.Clone()
	out.Proxy = nil
	return out
}

// checkHomeRedirect keeps the home page on the host it started from, and takes
// no more than homeRedirectLimit hops. http.ErrUseLastResponse hands the redirect
// back to the caller, which reports it by its status, so a refused hop is visible
// rather than silent.
func checkHomeRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= homeRedirectLimit {
		logging.Log.Warn("portal page redirect stopped after %d hops at %s", len(via), req.URL.Redacted())
		return http.ErrUseLastResponse
	}
	if from, to := via[0].URL.Hostname(), req.URL.Hostname(); !strings.EqualFold(from, to) {
		logging.Log.Warn("portal page redirect from %s to %s was not followed: the page may only be read from the host it is configured with",
			via[0].URL.Redacted(), req.URL.Redacted())
		return http.ErrUseLastResponse
	}
	return nil
}

// warnAboutTheTransport says once per client what the addresses it was given
// mean. None of it stops the program: the portal this was written for serves
// plain HTTP and refusing to run would leave the user offline. What the program
// can do is make the exposure visible in the log, where a user asking why their
// password leaked will look.
func (c *Client) warnAboutTheTransport() {
	portalHost := hostOf(c.cfg.PortalBaseURL)
	loginHost := hostOf(c.cfg.EportalURL)

	if loginHost != "" && !strings.EqualFold(loginHost, defaultPortalHost) {
		logging.Log.Warn("the login is configured for host %s and not the campus portal %s this program ships with: the credentials go to whoever answers that address",
			loginHost, defaultPortalHost)
	}
	if portalHost != "" && loginHost != "" && !strings.EqualFold(portalHost, loginHost) {
		logging.Log.Warn("the portal page is read from %s while the login is sent to %s: the two are different hosts, so the address the page reports cannot be checked against the one the credentials go to",
			portalHost, loginHost)
	}
	if !isTLS(c.cfg.EportalURL) {
		logging.Log.Warn("credentials are sent in clear text: %s is not https, so the account and the password travel the network readable, the password inside the URL query where every log along the path may keep it",
			c.cfg.EportalURL)
	}
	if names := proxyEnvironment(); len(names) > 0 {
		logging.Log.Warn("%s is set in the environment and is deliberately ignored: the portal is reached directly, so the credentials are never handed to a proxy",
			strings.Join(names, ", "))
	}
}

// hostOf is the host of a URL without its port, or empty when there is none.
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func isTLS(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "https")
}

// proxyEnvironment names the environment variables that would have routed the
// requests through a proxy, so the log says why they are not.
func proxyEnvironment() []string {
	var names []string
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			names = append(names, key)
		}
	}
	return names
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
			c.cfg.PortalBaseURL, code, c.cut(page))
	}
	// The page is text from a host that has not proved anything, and whatever
	// address it names is sent back as this client's. An address that is not one
	// is dropped, so the value in play is always an address.
	st.ClientIP = addressOrEmpty(firstMatch(reV4, page), "the portal page")
	if st.ClientIP == "" {
		st.ClientIP = addressOrEmpty(firstMatch(reV46, page), "the portal page (v46ip)")
	}
	st.ClientIPv6 = addressOrEmpty(firstMatch(reV6, page), "the portal page (v6ip)")
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
			st.ClientIP, code, c.cut(body))
		return st
	}

	result := firstMatch(reResult, body)
	if result == "" {
		st.Message = "online_list reply had no result field"
		logging.Log.Warn("online_list for client ip %q had no result field, leaving the session state unknown (body %s)",
			st.ClientIP, c.cut(body))
		return st
	}
	st.Known = true
	st.Online = result == "1" || strings.EqualFold(result, "ok")
	// The address and the session details come out of the reply and are printed
	// elsewhere with %s, so each one is made printable here: a newline in a value
	// would forge a whole line of the log, and an escape sequence would drive the
	// terminal of whoever reads it.
	if ip := addressOrEmpty(firstMatch(reOnlineIP, body), "online_list"); ip != "" {
		st.ClientIP = ip
	}
	if v6 := addressOrEmpty(firstMatch(reOnlineV6, body), "online_list (online_ipv6_1)"); v6 != "" {
		st.ClientIPv6 = v6
	}
	st.Account = cleanField(firstMatch(reUserAccount, body))
	st.MAC = cleanField(firstMatch(reOnlineMAC, body))
	st.OnlineSince = cleanField(firstMatch(reOnlineTime, body))
	logging.Log.Debug("online_list parsed: result=%q online=%v account=%q mac=%q since=%q ipv6=%q",
		result, st.Online, st.Account, st.MAC, st.OnlineSince, st.ClientIPv6)
	if !st.Online && st.ClientIP == "" {
		// A login would carry an empty wlan_user_ip: the request could not say
		// which client to authenticate, and neither the reply nor the page it came
		// with named one. Left as unknown rather than submitted, which is also the
		// answer a page that is not the portal earns, and the state says so.
		st.Known = false
		st.Message = "no client address is known, a login would send an empty wlan_user_ip"
		logging.Log.Warn("online_list says the session is offline and no client address is known, so no login is attempted this round; the portal page did not name one either")
		return st
	}
	if st.Online {
		st.Message = "online"
	} else {
		st.Message = "not logged in"
		logging.Log.Debug("online_list raw body: %s", c.cut(body))
	}
	return st
}

// Login submits the credentials of one attempt, mirroring what the login page
// does for login_method=1. It never logs out, so a failed attempt leaves the
// current session untouched.
func (c *Client) Login(st Status, attempt config.Attempt) LoginResult {
	var result LoginResult

	// The address is the one value of the login that comes out of a reply and
	// goes back out as written, so it is an address or nothing is sent: a reply
	// cannot put a string of its own choosing into the next request, and there is
	// no address to authenticate anyway.
	clientIP := validIP(st.ClientIP)
	if clientIP == "" && st.ClientIP != "" {
		result.Message = fmt.Sprintf("no login was sent: the portal reported the client address %q, which is not an IP address",
			cleanField(st.ClientIP))
		logging.Log.Warn("login skipped: %s", result.Message)
		return result
	}

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
	q.Set("wlan_user_ip", clientIP)
	q.Set("wlan_user_ipv6", st.ClientIPv6)
	q.Set("wlan_user_mac", mac)
	q.Set("wlan_ac_ip", "")
	q.Set("wlan_ac_name", "")
	q.Set("jsVersion", c.cfg.JsVersion)
	q.Set("v", strconv.Itoa(500+rand.Intn(9500)))

	logging.Log.Debug("login params: login_method=1 user_account=%q wlan_user_ip=%q wlan_user_ipv6=%q wlan_user_mac=%q jsVersion=%q",
		c.cfg.Account(attempt.Username, attempt.ISP), clientIP, st.ClientIPv6, mac, c.cfg.JsVersion)
	if clientIP == "" {
		// Kept as a submitted request for compatibility: the portal may fall back
		// to the source address of the request. It is said out loud, because a
		// page that does not name the client is also what a forged page looks
		// like, and this is the one case where a login is still worth trying.
		logging.Log.Warn("login for %q carries an empty wlan_user_ip: no client address was found on the portal page, so the portal has to fall back to the source address of the request",
			c.cfg.Account(attempt.Username, attempt.ISP))
	}

	code, body, err := c.get(c.plain, c.cfg.EportalURL+"?"+q.Encode(), attempt.Password)
	if err != nil {
		result.Message = "request failed: " + err.Error()
		logging.Log.Warn("login request failed: %v", err)
		return result
	}
	// The reply is kept for the log and for the settings page, and a person reads
	// both: the login is a GET with user_password in its query, so a reply that
	// echoes the request would put the secret in front of them. Mask it where it
	// is made, both the parameter name and the password itself, since a portal
	// that fails an authentication quotes the value without naming it.
	result.Raw = c.cut(body, attempt.Password)

	// online_list is judged by its status and this was not, so a reply the portal
	// never made could be read as an accepted login: the portal answers http 200
	// for an accepted login and for a refused one, and nothing else.
	if code != http.StatusOK && code != http.StatusNoContent {
		result.Message = fmt.Sprintf("login returned http %d, which is not a login answer", code)
		logging.Log.Warn("login for %q returned http %d, not read as an answer (body %s)",
			c.cfg.Account(attempt.Username, attempt.ISP), code, result.Raw)
		return result
	}

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
			result.Message = c.cut(msg, attempt.Password)
		} else {
			result.Message = fmt.Sprintf("result=%q, HTTP %d", res, code)
		}
	}
	logging.Log.Debug("login parsed: result=%q ret_code=%q success=%v message=%q body=%s",
		res, result.RetCode, result.Success, result.Message, result.Raw)
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

func (c *Client) get(client *http.Client, rawURL string, secrets ...string) (int, string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		logging.Log.Debug("http get %s: bad request: %v",
			c.safeURL(rawURL, secrets...), c.maskedError(err, secrets...))
		return 0, "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		err = c.maskedError(err, secrets...)
		logging.Log.Debug("http get %s failed after %d ms: %v", c.safeURL(rawURL, secrets...), elapsedMS(start), err)
		return 0, "", err
	}
	defer resp.Body.Close()

	// One byte past the limit tells the two cases apart: a reply of exactly the
	// limit was read whole, and a longer one was cut. The limit is applied to
	// what arrives, so a compressed reply that unpacks to more than this is cut
	// in the same place and never held in memory.
	data, err := io.ReadAll(io.LimitReader(resp.Body, replyLimit+1))
	if err != nil {
		logging.Log.Debug("http get %s: http %d but reading body failed: %v",
			c.safeURL(rawURL, secrets...), resp.StatusCode, c.maskedError(err, secrets...))
		return resp.StatusCode, "", err
	}
	if len(data) > replyLimit {
		logging.Log.Warn("the reply from %s is larger than the %d byte limit and was not read in full; the request is treated as failed rather than parsed from what was cut",
			c.safeURL(rawURL, secrets...), replyLimit)
		return resp.StatusCode, "", fmt.Errorf("%w of %d bytes", errReplyTooLarge, replyLimit)
	}
	logging.Log.Debug("http get %s -> http %d, %d bytes in %d ms",
		c.safeURL(rawURL, secrets...), resp.StatusCode, len(data), elapsedMS(start))
	return resp.StatusCode, string(data), nil
}

// maskedError is err with the password taken out of the address it names. A
// url.Error repeats the address the request was made with, and the login puts
// the password in that address, so the error text is the third place the
// password can come back: the reply, the request line, and this one. Masking it
// where the request is made covers every line that prints it, the settings page
// and the notification.
func (c *Client) maskedError(err error, secrets ...string) error {
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}
	masked := c.maskSecrets(uerr.URL, secrets...)
	if masked == uerr.URL {
		return err
	}
	// A copy, so the caller's error is left as it was made and the chain the
	// request built is kept for anything that looks inside it.
	out := *uerr
	out.URL = masked
	return &out
}

// safeURL removes the password from a request URL before it is logged.
func (c *Client) safeURL(rawURL string, secrets ...string) string {
	return c.maskSecrets(rawURL, secrets...)
}

// MaskSecrets removes the password from a string before it is logged, since a
// caller may print the portal's own reply. Masking by parameter name covers
// every account in the login list; user_account is left readable.
func MaskSecrets(s string) string {
	return rePassword.ReplaceAllString(s, "${1}***")
}

// maskSecrets masks a reply or a URL twice over: by parameter name, and by the
// value of every password this client knows. A portal that fails an
// authentication quotes what it was given without necessarily naming
// user_password, and that quote reaches the log and the settings page.
func (c *Client) maskSecrets(s string, extra ...string) string {
	return maskValues(MaskSecrets(s), c.passwordValues(extra...))
}

// passwordValues is every password this client could have to hide: the ones the
// configuration holds, and the one the attempt being submitted carries.
func (c *Client) passwordValues(extra ...string) []string {
	var out []string
	if c.cfg != nil {
		for _, a := range c.cfg.Accounts {
			out = append(out, a.Password)
		}
	}
	return append(out, extra...)
}

// maskValues replaces each value in s, in the spelling it appears in and in the
// spelling a query string would escape it to. Values too short to be a password
// are left alone, see minMaskedSecretLen.
func maskValues(s string, values []string) string {
	for _, v := range values {
		if len(v) < minMaskedSecretLen {
			continue
		}
		s = strings.ReplaceAll(s, v, "***")
		if escaped := url.QueryEscape(v); escaped != v {
			s = strings.ReplaceAll(s, escaped, "***")
		}
	}
	return s
}

// cut is Truncate for a string that came out of the portal, so the passwords of
// the whole login list are taken out of it and not only the one the parameters
// name.
func (c *Client) cut(s string, extra ...string) string {
	return Truncate(c.maskSecrets(s, extra...))
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

// addressOrEmpty is the address in raw, or empty when the value is not an
// address at all. The value is read with a regular expression that allows any
// byte but a quote, and it is used for the requests the program makes next, so
// what is not an address is named in the log and dropped.
func addressOrEmpty(raw, source string) string {
	if raw == "" {
		return ""
	}
	if ip := validIP(raw); ip != "" {
		return ip
	}
	logging.Log.Warn("%s reported the client address %q, which is not an IP address, so it is ignored", source, cleanField(raw))
	return ""
}

// validIP is s when it is an address and empty otherwise. See addressOrEmpty.
func validIP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || net.ParseIP(s) == nil {
		return ""
	}
	return s
}

// cleanField makes a value that came out of a portal reply safe to interpolate
// with %s into a log line or into the interface: a newline in it would forge a
// whole log line, and an escape sequence would drive the terminal of whoever
// runs tail -f. The reply fields are read with a regular expression that allows
// any byte but a quote, so this is where they are filtered.
func cleanField(s string) string {
	if s == "" {
		return ""
	}
	out := strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7f, 0x80 <= r && r <= 0x9f:
			return -1
		}
		return r
	}, s)
	return cutToLimit(strings.TrimSpace(out), truncateLimit)
}

// truncateLimit is how much of a portal reply is kept. It is a byte count, so a
// line of Chinese text is cut shorter than a line of Latin text.
const truncateLimit = 300

// Truncate collapses whitespace, removes the password, and cuts s to a length a
// log line can carry. The mask belongs here and not at each call site: the login
// is a GET with user_password in its query, so a reply that echoes it puts the
// secret wherever that reply is printed, the settings page included.
func Truncate(s string) string {
	s = MaskSecrets(strings.Join(strings.Fields(s), " "))
	return cutToLimit(s, truncateLimit)
}

// cutToLimit cuts s to at most limit bytes. The cut is moved back to a character
// boundary: cutting a multi-byte character in half would write a broken sequence
// into the log, and log lines are read as text by whatever tail tool the user
// has.
func cutToLimit(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
