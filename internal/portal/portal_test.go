package portal

import (
	"bytes"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"sdautologin/internal/config"
	"sdautologin/internal/logging"
)

const portalHome = `<html><script>var v4ip = '203.0.113.7'; var v6ip = '2001:db8::1';</script></html>`

// testConfig points both portal endpoints at one stub server.
func testConfig(base string) *config.Config {
	return &config.Config{
		Accounts: []config.Account{
			{Username: "s1234567", Password: "p@ss", ISPs: []string{"@zgyd"}},
		},
		AccountPrefix:      ",0,",
		PortalBaseURL:      base + "/",
		EportalURL:         base + "/eportal/",
		HTTPTimeoutSeconds: 5,
		JsVersion:          "3.3.3",
	}
}

// testAttempt is the one attempt the test configuration asks for.
func testAttempt() config.Attempt {
	return config.Attempt{Index: 0, Username: "s1234567", Password: "p@ss", ISP: "@zgyd"}
}

// closedBase is the address of a server that has been shut down again, so a
// request to it fails the way an unreachable portal does.
func closedBase(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close()
	return base
}

// stubPortal answers the two endpoints the client uses. Each field steers one
// answer, and the requests are counted so a test can also assert what was sent.
type stubPortal struct {
	onlineList       string
	onlineListStatus int
	loginReply       string
	loginStatus      int
	// redirectOnline answers online_list with a redirect instead, the way a
	// captive portal intercepts a request.
	redirectOnline bool

	mu      sync.Mutex
	actions []string
	logins  int
	loginQV url.Values
}

func (s *stubPortal) start(t *testing.T) *config.Config {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, portalHome)
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		action := r.URL.Query().Get("a")
		s.actions = append(s.actions, action)

		if action == "login" {
			s.logins++
			s.loginQV = r.URL.Query()
			if s.loginStatus != 0 {
				w.WriteHeader(s.loginStatus)
			}
			_, _ = io.WriteString(w, s.loginReply)
			return
		}
		if s.redirectOnline {
			http.Redirect(w, r, "/eportal/?a=online_list&redirected=1", http.StatusFound)
			return
		}
		if s.onlineListStatus != 0 {
			w.WriteHeader(s.onlineListStatus)
		}
		_, _ = io.WriteString(w, s.onlineList)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return testConfig(srv.URL)
}

func (s *stubPortal) lastLoginQuery() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loginQV
}

func (s *stubPortal) seenActions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.actions...)
}

func TestQueryStatusReportsAnOnlineSession(t *testing.T) {
	s := &stubPortal{onlineList: `{"result":1,"user_account":"s1234567","online_ip":"203.0.113.9",` +
		`"online_mac":"aabbccddeeff","online_time":"2026-01-01 00:00:00","online_ipv6_1":"2001:db8::2"}`}

	st := NewClient(s.start(t)).QueryStatus()

	if !st.Reachable || !st.Known || !st.Online {
		t.Fatalf("Status = %+v, want a reachable online session", st)
	}
	// The address online_list reports is the authenticated one and wins over the
	// one the home page embeds.
	if st.ClientIP != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want the address online_list reported", st.ClientIP)
	}
	if st.ClientIPv6 != "2001:db8::2" {
		t.Errorf("ClientIPv6 = %q", st.ClientIPv6)
	}
	if st.Account != "s1234567" || st.MAC != "aabbccddeeff" || st.OnlineSince != "2026-01-01 00:00:00" {
		t.Errorf("Status = %+v, want the account, mac and session start", st)
	}
	if st.Message != "online" {
		t.Errorf("Message = %q", st.Message)
	}
}

func TestQueryStatusFallsBackToTheAddressOnTheHomePage(t *testing.T) {
	// Before a session exists online_list has no address to report, so the one
	// embedded in the home page is the only one available.
	s := &stubPortal{onlineList: `{"result":0}`}

	st := NewClient(s.start(t)).QueryStatus()

	if st.ClientIP != "203.0.113.7" {
		t.Errorf("ClientIP = %q, want the v4ip from the home page", st.ClientIP)
	}
	if st.ClientIPv6 != "2001:db8::1" {
		t.Errorf("ClientIPv6 = %q, want the v6ip from the home page", st.ClientIPv6)
	}
}

func TestQueryStatusReportsAnOfflineSession(t *testing.T) {
	s := &stubPortal{onlineList: `{"result":0}`}

	st := NewClient(s.start(t)).QueryStatus()

	if !st.Reachable || !st.Known {
		t.Fatalf("Status = %+v, want a readable reply", st)
	}
	if st.Online {
		t.Error("Online = true although the portal reported result 0")
	}
	if st.Message != "not logged in" {
		t.Errorf("Message = %q", st.Message)
	}
}

func TestQueryStatusAcceptsAnOKResult(t *testing.T) {
	s := &stubPortal{onlineList: `{"result":"ok"}`}

	if st := NewClient(s.start(t)).QueryStatus(); !st.Online {
		t.Errorf("Status = %+v, want ok to count as online", st)
	}
}

func TestQueryStatusWithoutAResultLeavesTheStateUnknown(t *testing.T) {
	s := &stubPortal{onlineList: `{"msg":"something a captive portal sent"}`}

	st := NewClient(s.start(t)).QueryStatus()

	if !st.Reachable {
		t.Error("Reachable = false although the portal answered")
	}
	if st.Known {
		t.Error("Known = true although the reply had no result field")
	}
	if st.Online {
		t.Error("Online = true although the reply had no result field")
	}
	if !strings.Contains(st.Message, "no result field") {
		t.Errorf("Message = %q", st.Message)
	}
}

func TestQueryStatusWithAServerErrorLeavesTheStateUnknown(t *testing.T) {
	s := &stubPortal{onlineList: "<html>oops</html>", onlineListStatus: http.StatusInternalServerError}

	st := NewClient(s.start(t)).QueryStatus()

	if st.Known {
		t.Error("Known = true although online_list returned an error status")
	}
	if !strings.Contains(st.Message, "500") {
		t.Errorf("Message = %q, want the status code in it", st.Message)
	}
}

func TestQueryStatusDoesNotFollowARedirect(t *testing.T) {
	// The redirect target answers a valid online_list reply, so following it
	// would make an interception look like a working portal.
	s := &stubPortal{redirectOnline: true}

	st := NewClient(s.start(t)).QueryStatus()

	if st.Known {
		t.Error("Known = true, the redirect was followed")
	}
	if !strings.Contains(st.Message, "302") {
		t.Errorf("Message = %q, want the redirect to be reported by its status", st.Message)
	}
}

func TestQueryStatusReportsAnUnreachablePortal(t *testing.T) {
	cfg := testConfig(closedBase(t))

	st := NewClient(cfg).QueryStatus()

	if st.Reachable || st.Known || st.Online {
		t.Errorf("Status = %+v, want nothing to be trusted", st)
	}
	if !strings.Contains(st.Message, "portal unreachable") {
		t.Errorf("Message = %q", st.Message)
	}
}

func TestLoginAccepted(t *testing.T) {
	s := &stubPortal{loginReply: `{"result":1,"ret_code":"0"}`}

	res := NewClient(s.start(t)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if !res.Success {
		t.Fatalf("Login = %+v, want success", res)
	}
	if res.RetCode != "0" {
		t.Errorf("RetCode = %q", res.RetCode)
	}
	if res.Message != "login accepted" {
		t.Errorf("Message = %q", res.Message)
	}
	if res.Raw == "" {
		t.Error("Raw is empty, the reply is needed to diagnose a failure")
	}
}

func TestLoginRejectedWithAKnownRetCode(t *testing.T) {
	s := &stubPortal{loginReply: `{"result":0,"ret_code":"1"}`}

	res := NewClient(s.start(t)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if res.Success {
		t.Fatalf("Login = %+v, want a rejection", res)
	}
	if res.RetCode != "1" {
		t.Errorf("RetCode = %q", res.RetCode)
	}
	if res.Message != "wrong account or password" {
		t.Errorf("Message = %q, want the documented wording of ret_code 1", res.Message)
	}
}

func TestLoginUsesThePortalMessageForAnUnknownRetCode(t *testing.T) {
	// The portal escapes the Chinese text, and \u7528\u6237\u4e0d\u5b58\u5728 is
	// "user does not exist". The client has to resolve that before showing it.
	s := &stubPortal{loginReply: `{"result":0,"ret_code":"99","msg":"\u7528\u6237\u4e0d\u5b58\u5728"}`}

	res := NewClient(s.start(t)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if want := "\u7528\u6237\u4e0d\u5b58\u5728"; res.Message != want {
		t.Errorf("Message = %q, want %q", res.Message, want)
	}
}

func TestLoginSendsTheExpectedParameters(t *testing.T) {
	s := &stubPortal{loginReply: `{"result":1,"ret_code":"0"}`}

	NewClient(s.start(t)).Login(Status{
		ClientIP:   "203.0.113.7",
		ClientIPv6: "2001:db8::1",
		MAC:        "aabbccddeeff",
	}, testAttempt())

	q := s.lastLoginQuery()
	if q == nil {
		t.Fatal("no login request reached the portal")
	}
	want := map[string]string{
		"c":              "Portal",
		"a":              "login",
		"login_method":   "1",
		"user_account":   ",0,s1234567@zgyd",
		"user_password":  "p@ss",
		"wlan_user_ip":   "203.0.113.7",
		"wlan_user_ipv6": "2001:db8::1",
		"wlan_user_mac":  "aabbccddeeff",
		"jsVersion":      "3.3.3",
	}
	for key, value := range want {
		if got := q.Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if q.Get("callback") == "" || q.Get("v") == "" {
		t.Error("the parameters that keep the portal from serving a cached reply are missing")
	}
}

func TestLoginFallsBackToAZeroMAC(t *testing.T) {
	// The status comes from a query that may have failed, so a login must still
	// produce a usable request.
	s := &stubPortal{loginReply: `{"result":1,"ret_code":"0"}`}

	NewClient(s.start(t)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if got := s.lastLoginQuery().Get("wlan_user_mac"); got != "000000000000" {
		t.Errorf("wlan_user_mac = %q, want the zero mac", got)
	}
}

func TestLoginReportsAFailedRequest(t *testing.T) {
	res := NewClient(testConfig(closedBase(t))).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if res.Success {
		t.Error("Success = true although the request could not be sent")
	}
	if !strings.Contains(res.Message, "request failed") {
		t.Errorf("Message = %q", res.Message)
	}
}

func TestTheClientNeverLogsOut(t *testing.T) {
	// A logout would leave the machine offline, the outcome this program exists
	// to prevent.
	s := &stubPortal{onlineList: `{"result":0}`, loginReply: `{"result":1,"ret_code":"0"}`}
	client := NewClient(s.start(t))

	st := client.QueryStatus()
	client.Login(st, testAttempt())
	client.QueryStatus()
	client.ProbeInternet()

	for _, action := range s.seenActions() {
		if action == "logout" {
			t.Fatal("the client asked the portal to log out")
		}
	}
}

func TestProbeInternetAcceptsAnExpectedNoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.ProbeURLs = []string{srv.URL + "/generate_204"}

	if !NewClient(cfg).ProbeInternet() {
		t.Error("ProbeInternet = false, want an expected 204 to count as reachable")
	}
}

func TestProbeInternetRejectsAnInterceptedReply(t *testing.T) {
	// A captive portal answers 200 where the probe expected 204 and injects its
	// own page, which must not be mistaken for a working connection.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<html>Dr.COM eportal</html>")
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.ProbeURLs = []string{srv.URL + "/generate_204"}

	if NewClient(cfg).ProbeInternet() {
		t.Error("ProbeInternet = true although the reply was intercepted")
	}
}

func TestProbeInternetWithoutProbesIsNotAFailure(t *testing.T) {
	cfg := testConfig(closedBase(t))
	cfg.ProbeURLs = nil

	if !NewClient(cfg).ProbeInternet() {
		t.Error("ProbeInternet = false, want an unchecked Internet to be reported as fine")
	}
}

func TestTruncateCollapsesWhitespaceAndCutsLongText(t *testing.T) {
	if got := Truncate("  a\n\tb  "); got != "a b" {
		t.Errorf("Truncate = %q, want %q", got, "a b")
	}

	long := strings.Repeat("x", 400)
	got := Truncate(long)
	if len(got) != 300+len("...") || !strings.HasSuffix(got, "...") {
		t.Errorf("Truncate kept %d characters, want 300 plus the ellipsis", len(got))
	}
}

func TestTruncateCutsOnACharacterBoundary(t *testing.T) {
	// Cut by bytes and a multi-byte character at the seam becomes a broken
	// sequence in the log; the result has to stay valid text and keep the same
	// ellipsis.
	long := strings.Repeat("\u597d", 200) // 600 bytes, cut would land mid-character

	got := Truncate(long)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("Truncate = %q, want the ellipsis", got)
	}
	body := strings.TrimSuffix(got, "...")
	if !utf8.ValidString(body) {
		t.Errorf("Truncate left a broken character: %q", body)
	}
	if len(body) > 300 {
		t.Errorf("Truncate kept %d bytes, want at most 300", len(body))
	}
	// Nothing was dropped beyond the cut, so the kept text is the prefix.
	if !strings.HasPrefix(long, body) {
		t.Error("Truncate did not keep a prefix of the input")
	}
}

func TestLoggedURLsNeverContainThePassword(t *testing.T) {
	// The login request carries the password in its query string and that URL is
	// written to the log, so it has to be masked first; see MaskSecrets.
	client := NewClient(&config.Config{})
	secret := "p@ss@word"
	raw := "http://portal/eportal/?a=login&user_password=" + url.QueryEscape(secret)

	got := client.safeURL(raw)
	if strings.Contains(got, url.QueryEscape(secret)) || strings.Contains(got, secret) {
		t.Errorf("safeURL left the password in place: %s", got)
	}
	if !strings.Contains(got, "user_password=***") {
		t.Errorf("safeURL = %s, want the parameter replaced", got)
	}
	// The rest of the request has to survive, or the log line says nothing.
	if !strings.Contains(got, "a=login") {
		t.Errorf("safeURL = %s, want the other parameters left alone", got)
	}
}

func TestLoginTruncatesALongPortalMessage(t *testing.T) {
	// A portal that answers with a whole page must not put it all on the page and
	// in the tooltip, so the message goes through the same cut as the raw reply.
	long := strings.Repeat("z", 400)
	s := &stubPortal{loginReply: `{"result":0,"ret_code":"99","msg":"` + long + `"}`}

	res := NewClient(s.start(t)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if res.Message != Truncate(long) {
		t.Errorf("Message kept %d bytes, want the same cut as Truncate", len(res.Message))
	}
	if len(res.Message) > truncateLimit+len("...") || !strings.HasSuffix(res.Message, "...") {
		t.Errorf("Message = %q, want it cut to a readable length", res.Message)
	}
}

func TestMaskSecretsHidesThePasswordInAURL(t *testing.T) {
	raw := "http://portal/eportal/?a=login&user_account=" + url.QueryEscape(",0,s1234567@zgyd") +
		"&user_password=" + url.QueryEscape("p@ss@word")

	got := MaskSecrets(raw)
	if strings.Contains(got, url.QueryEscape("p@ss@word")) || strings.Contains(got, "p@ss@word") {
		t.Errorf("MaskSecrets left the password in place: %s", got)
	}
	if !strings.Contains(got, "user_password=***") {
		t.Errorf("MaskSecrets = %s, want the parameter replaced", got)
	}
	// user_account is a login name, not a secret, and is deliberately logged.
	if !strings.Contains(got, "user_account="+url.QueryEscape(",0,s1234567@zgyd")) {
		t.Errorf("MaskSecrets = %s, want user_account left alone", got)
	}
}

func TestMaskSecretsHidesThePasswordInARawReply(t *testing.T) {
	// A portal can echo the request back, so a caller that prints the reply
	// needs the same masking that safeURL applies to a URL.
	raw := "reply: a=login&user_password=secret&user_account=,0,s1"

	got := MaskSecrets(raw)
	if strings.Contains(got, "secret") {
		t.Errorf("MaskSecrets left the password in place: %s", got)
	}
	if !strings.Contains(got, "user_password=***&user_account=,0,s1") {
		t.Errorf("MaskSecrets = %s, want only the password replaced", got)
	}
}

// A reply is the one string that can carry the password back. The login is a
// GET with user_password in the query, so a portal or a proxy that echoes the
// request in an error page puts the secret into every log line and into the
// settings page, which are both handed Truncate's output.
func TestTruncateRemovesThePasswordFromAReply(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"the query the request was made with", `http://portal/?a=login&user_account=,0,s1&user_password=p%40ss&x=1`},
		{"a JSON echo", `{"ret_code":1,"user_password":"p@ss","msg":"bad"}`},
		{"a single quoted echo", `{'user_password': 'p@ss', 'ret_code': 1}`},
		{"a spaced echo", `user_password : p@ss  ret_code 1`},
		{"a query with no other parameter", `user_password=p@ss`},
		{"an escaped quote inside the password", `{"user_password":"p\"ss","msg":"bad"}`},
		{"the key in capitals", `USER_PASSWORD=p@ss&a=1`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Truncate(c.raw)
			for _, secret := range []string{"p@ss", "p%40ss", `p\"ss`} {
				if strings.Contains(got, secret) {
					t.Errorf("Truncate left %q in place: %s", secret, got)
				}
			}
			if !strings.Contains(got, "***") {
				t.Errorf("Truncate = %s, want the value replaced", got)
			}
		})
	}
}

// The rest of the reply has to survive the mask, or the log line and the test
// report say nothing about what the portal actually answered.
func TestTruncateKeepsEverythingButThePassword(t *testing.T) {
	got := Truncate(`http://portal/?a=login&user_account=,0,s1234567&user_password=p%40ss&x=1`)
	for _, want := range []string{"a=login", "user_account=,0,s1234567", "x=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("Truncate = %s, want %q kept", got, want)
		}
	}
	if got := Truncate(`{"ret_code":1,"msg":"the account is not registered"}`); !strings.Contains(got, "the account is not registered") {
		t.Errorf("Truncate = %s, want a reply with no password left alone", got)
	}
}

// LoginResult.Raw is not only logged: the settings page is handed the whole
// value and renders it, so a portal or a proxy that answers an error page echoing
// the request would put the password in front of whoever opened the page. The
// mask is applied where the value is made, and that covers both.
func TestTheKeptReplyHasNoPasswordInIt(t *testing.T) {
	// A proxy that repeats the URL it was asked for. The login is a GET with
	// user_password in the query, so that is where the password comes back from.
	s := &stubPortal{loginReply: `{"result":0,"ret_code":"1","echo":"/eportal/?a=login&user_account=,0,s1234567&user_password=p%40ss"}`}

	res := NewClient(s.start(t)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if strings.Contains(res.Raw, "p%40ss") || strings.Contains(res.Raw, "p@ss") {
		t.Errorf("Raw carries the password: %s", res.Raw)
	}
	if !strings.Contains(res.Raw, "user_password=***") {
		t.Errorf("Raw = %s, want the parameter masked", res.Raw)
	}
	// The rest of the reply has to survive, or the report says nothing about what
	// the portal answered.
	if !strings.Contains(res.Raw, "ret_code") {
		t.Errorf("Raw = %s, want the rest of the reply kept", res.Raw)
	}
}

// A reply with nothing to hide is kept as it is, up to the length a line can
// carry: the page shows this text, and a portal that answers a novel would push
// everything else off it.
func TestTheKeptReplyIsCutToALine(t *testing.T) {
	long := strings.Repeat("x", 4000)
	s := &stubPortal{loginReply: `{"result":0,"ret_code":"1","msg":"` + long + `"}`}

	res := NewClient(s.start(t)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if len(res.Raw) > 400 {
		t.Errorf("Raw is %d bytes, want it cut to something a log line can carry", len(res.Raw))
	}
}

// A request that fails carries the address it was made with inside the error,
// and the login puts the password in that address. The error text is the third
// place the password can come back, after the reply and the request line, and
// it reaches the log, the settings page and the notification.
func TestALoginThatFailsDoesNotCarryThePassword(t *testing.T) {
	dir := t.TempDir()
	logging.Log.Init(dir, true)
	t.Cleanup(logging.Log.CloseFile)

	res := NewClient(testConfig(closedBase(t))).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	// The address is escaped by the time it is in the query, so both spellings
	// are looked for.
	for _, secret := range []string{"p@ss", "p%40ss"} {
		if strings.Contains(res.Message, secret) {
			t.Errorf("the failure message carries %q:\n%s", secret, res.Message)
		}
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("the run wrote no log file")
	}
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"p@ss", "p%40ss"} {
			if strings.Contains(string(data), secret) {
				t.Errorf("the log carries %q:\n%s", secret, data)
			}
		}
	}
}

// stubEndpoints answers the two endpoints with the bodies a test chooses, for the
// cases the shared stub does not cover: a page without an address in it, and a
// page that is not a page.
func stubEndpoints(t *testing.T, home, onlineList string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, home)
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, onlineList)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// logDir puts the package logger on a file of its own for one test, so what the
// test made the client say can be read back.
func logDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	logging.Log.Init(dir, true)
	t.Cleanup(logging.Log.CloseFile)
	logging.Log.SetVerbose(true)
	t.Cleanup(func() { logging.Log.SetVerbose(false) })
	return dir
}

// readLog is everything the logger wrote in dir, in one string.
func readLog(t *testing.T, dir string) string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("the run wrote no log file")
	}
	var out strings.Builder
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(data)
	}
	return out.String()
}

// The portal this client was written for answers plain HTTP and nothing here can
// change that, so what the client can do is say what the configuration means:
// which host the credentials go to, that the password travels readable, and which
// host the page is read from when it is not the one the login is sent to.
func TestTheTransportExposureIsAnnounced(t *testing.T) {
	dir := logDir(t)

	// The shipped portal over https: nothing to warn about.
	NewClient(testConfig("https://" + defaultPortalHost))
	if log := readLog(t, dir); strings.Contains(log, "clear text") {
		t.Errorf("an https portal was reported as clear text:\n%s", log)
	}

	// The same portal over http: this is the exposure the log has to name.
	NewClient(testConfig("http://" + defaultPortalHost))
	log := readLog(t, dir)
	if !strings.Contains(log, "clear text") || !strings.Contains(log, defaultPortalHost) {
		t.Errorf("the log does not say the credentials travel in clear text:\n%s", log)
	}

	// A host that is not the campus portal the program ships with.
	NewClient(testConfig("http://portal.example"))
	log = readLog(t, dir)
	if !strings.Contains(log, "portal.example") || !strings.Contains(log, "not the campus portal") {
		t.Errorf("the log does not name the unexpected host:\n%s", log)
	}

	// A page read from one host while the login goes to another: the address the
	// page reports can then not be checked against where the credentials go.
	cfg := testConfig("http://portal.example")
	cfg.EportalURL = "http://login.example/eportal/"
	NewClient(cfg)
	log = readLog(t, dir)
	if !strings.Contains(log, "login.example") || !strings.Contains(log, "different hosts") {
		t.Errorf("the log does not separate the page host from the login host:\n%s", log)
	}
}

// The login carries the password in its URL, so a proxy taken from the
// environment would be handed the credentials. The client builds its own
// transport with the proxy left unset, and this test shows it on the traffic: a
// listener named by HTTP_PROXY must see nothing while the portal sees the login.
//
// The stub portal is reached by this machine's own LAN address rather than over
// the loopback, because Go never sends a loopback address to a proxy: on the
// loopback a proxied request and a direct one cannot be told apart.
func TestPortalRequestsDoNotGoThroughTheEnvironmentProxy(t *testing.T) {
	addr := localIPv4()
	if addr == "" {
		t.Skip("this machine has no non-loopback IPv4 address to reach a stub portal on")
	}

	var proxied int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&proxied, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()

	var (
		mu      sync.Mutex
		logins  int
		loginQV url.Values
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, portalHome)
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("a") == "login" {
			mu.Lock()
			logins++
			loginQV = r.URL.Query()
			mu.Unlock()
		}
		_, _ = io.WriteString(w, `{"result":1,"ret_code":"0"}`)
	})

	srv := httptest.NewUnstartedServer(mux)
	ln, err := net.Listen("tcp", addr+":0")
	if err != nil {
		t.Skipf("cannot listen on %s: %v", addr, err)
	}
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	// Reached directly first and without a proxy, so a machine that cannot route
	// to its own address says so instead of failing the assertions below. The
	// transport is built by hand: the default one would resolve the environment
	// proxy here and cache the answer for the rest of the process.
	direct := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{}}
	if resp, err := direct.Get(srv.URL + "/"); err != nil {
		t.Skipf("this machine cannot reach its own address %s: %v", srv.URL, err)
	} else {
		_ = resp.Body.Close()
	}

	// Both spellings and the https pair: a request for an http URL uses the
	// first, and Go reads the environment once for the whole process.
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)

	client := NewClient(testConfig(srv.URL))

	// Reported rather than fatal: the assertion that decides this test is the
	// traffic below, and this one names the reason when the transport is wrong.
	if tr, ok := client.plain.Transport.(*http.Transport); !ok || tr.Proxy != nil {
		t.Errorf("the portal client does not disable the environment proxy: %#v", client.plain.Transport)
	}

	res := client.Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if got := atomic.LoadInt32(&proxied); got != 0 {
		t.Errorf("the proxy received %d requests, want none: the credentials must not be handed to it", got)
	}
	mu.Lock()
	seen, q := logins, loginQV
	mu.Unlock()
	if seen != 1 {
		t.Fatalf("the portal saw %d logins, want 1: the login has to reach the portal itself", seen)
	}
	if q == nil || q.Get("user_password") != "p@ss" {
		t.Errorf("user_password = %q, want the password sent to the portal itself", q.Get("user_password"))
	}
	if !res.Success {
		t.Errorf("Login = %+v, want the portal's answer", res)
	}

	// The control: the same address through the standard transport does reach the
	// listener, so this address is one the environment proxy applies to and the
	// count above is a property of the client and not of a listener nothing can
	// reach.
	control, err := (&http.Client{Timeout: 5 * time.Second}).Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("the control request through the standard transport failed: %v", err)
	}
	_ = control.Body.Close()
	if got := atomic.LoadInt32(&proxied); got == 0 {
		t.Error("the standard transport did not reach the proxy either, so this test cannot tell the two apart")
	}
	t.Logf("portal %s and proxy %s: the portal saw the login, the proxy saw only the control request",
		srv.URL, proxy.URL)
}

// localIPv4 is this machine's first non-loopback IPv4 address, which is an
// address Go will send to an environment proxy.
func localIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil && !ipnet.IP.IsLoopback() {
			return ip4.String()
		}
	}
	return ""
}

// A portal configured over https is verified like any other TLS connection.
// httptest signs with its own authority, so the handshake has to fail and the
// password must not leave the machine even on the way to failing.
func TestALoginOverTLSWithAnUntrustedCertificateSendsNothing(t *testing.T) {
	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = io.WriteString(w, `{"result":1,"ret_code":"0"}`)
	}))
	defer srv.Close()

	client := NewClient(testConfig(srv.URL))

	res := client.Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if res.Success {
		t.Fatalf("Login = %+v, want the untrusted certificate refused", res)
	}
	if !strings.Contains(res.Message, "certificate") {
		t.Errorf("Message = %q, want the certificate failure in it", res.Message)
	}
	for _, secret := range []string{"p@ss", "p%40ss"} {
		if strings.Contains(res.Message, secret) {
			t.Errorf("the failure message carries %q: %s", secret, res.Message)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("the TLS server handled %d requests, want none: nothing may be sent before the certificate is verified", got)
	}
	if st := client.QueryStatus(); st.Reachable {
		t.Errorf("Status = %+v, want the portal page to be unreachable", st)
	}
	// Nothing turns verification off, which is the only thing that makes an https
	// address worth anything.
	if tr, ok := client.plain.Transport.(*http.Transport); ok && tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("the portal transport skips certificate verification")
	}
}

// A portal that accepts the connection and then says nothing has to be given up
// on: the request timeout is the only thing that ends it, and the answer has to
// be "unreachable" rather than a reply that never came.
func TestARequestThatTimesOutIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The client gives up on the request and cancels it, which is what ends
		// this handler: the server is not left waiting for a reply nobody wants.
		<-r.Context().Done()
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	// One second rather than the two the configuration allows, so the test does
	// not spend longer on it than it has to.
	cfg.HTTPTimeoutSeconds = 1

	st := NewClient(cfg).QueryStatus()

	if st.Reachable || st.Known || st.Online {
		t.Errorf("Status = %+v, want nothing to be trusted after a timeout", st)
	}
	if !strings.Contains(st.Message, "portal unreachable") {
		t.Errorf("Message = %q", st.Message)
	}
	if !strings.Contains(strings.ToLower(st.Message), "timeout") {
		t.Errorf("Message = %q, want the timeout named", st.Message)
	}
}

// The reply limit is a defense, and it has to be a diagnosable one: a reply that
// was cut off part-way is refused instead of parsed, so a "result":1 placed past
// the cut cannot be read as an answer.
func TestALoginReplyOverTheLimitIsRefused(t *testing.T) {
	s := &stubPortal{loginReply: strings.Repeat("x", replyLimit+1024) + `{"result":1,"ret_code":"0"}`}

	res := NewClient(s.start(t)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if res.Success {
		t.Fatalf("Login = %+v, want the oversized reply refused", res)
	}
	if !strings.Contains(res.Message, "read limit") {
		t.Errorf("Message = %q, want the limit named", res.Message)
	}
	if res.Raw != "" {
		t.Errorf("Raw = %q, want nothing kept from a reply that was not read in full", res.Raw)
	}
}

// A portal may compress its answers. The client sets no Accept-Encoding of its
// own, so Go adds one and unpacks the reply, and the pages have to be read all
// the same.
func TestAGzippedReplyIsReadTransparently(t *testing.T) {
	mux := http.NewServeMux()
	writeGzip := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = io.WriteString(zw, body)
		_ = zw.Close()
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { writeGzip(w, portalHome) })
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, _ *http.Request) {
		writeGzip(w, `{"result":1,"user_account":"s1234567"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st := NewClient(testConfig(srv.URL)).QueryStatus()

	if !st.Known || !st.Online {
		t.Fatalf("Status = %+v, want the compressed reply read", st)
	}
	if st.ClientIP != "203.0.113.7" {
		t.Errorf("ClientIP = %q, want the address from the compressed page", st.ClientIP)
	}
}

// A compressed reply that unpacks past the limit is cut at the same place: the
// limit is applied to what arrives decompressed, so a small body on the wire
// cannot make the client hold a large one.
func TestAGzippedReplyIsCutAtTheReadLimit(t *testing.T) {
	// A few kilobytes on the wire that expand past the read limit.
	packed := &bytes.Buffer{}
	zw := gzip.NewWriter(packed)
	_, _ = zw.Write(bytes.Repeat([]byte("x"), replyLimit+1024))
	_, _ = zw.Write([]byte(`{"result":1,"ret_code":"0"}`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if packed.Len() > 64<<10 {
		t.Fatalf("the packed reply is %d bytes, want a small one so the test shows the limit is not the size on the wire", packed.Len())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, portalHome) })
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(packed.Bytes())
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := NewClient(testConfig(srv.URL)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if res.Success {
		t.Fatalf("Login = %+v, want the packed reply past the limit refused", res)
	}
	if !strings.Contains(res.Message, "read limit") {
		t.Errorf("Message = %q, want the limit named", res.Message)
	}
}

// Every field of a reply is text from a host that has proved nothing, and the
// values are printed with %s by the callers, the log and the settings page. A
// newline in one of them would make the log say something the program never
// said, and an escape sequence would drive the terminal of whoever reads it.
func TestHostileReplyFieldsCannotForgeALogLine(t *testing.T) {
	dir := logDir(t)
	const (
		nl    = "\n"
		esc   = "\x1b"
		forge = "forged-line-xyz"
	)

	s := &stubPortal{onlineList: `{"result":0,` +
		`"online_ip":"203.0.113.9` + nl + `2026-01-01 00:00:00 [ERROR] ` + forge + `",` +
		`"user_account":"s1` + esc + `[2J` + nl + forge + `",` +
		`"online_mac":"aa` + nl + `bb",` +
		`"online_time":"2026-01-01 00:00:00` + nl + forge + `"}`}

	st := NewClient(s.start(t)).QueryStatus()

	// The address is the one value that goes back out in a request, and this is
	// not an address, so it is dropped rather than cleaned: the address the page
	// itself carried is what remains.
	if st.ClientIP != "203.0.113.7" {
		t.Errorf("ClientIP = %q, want the hostile value dropped and the page's own address kept", st.ClientIP)
	}
	for _, field := range []struct{ name, value string }{
		{"Account", st.Account},
		{"MAC", st.MAC},
		{"OnlineSince", st.OnlineSince},
	} {
		if strings.ContainsAny(field.value, "\n\r\x1b") {
			t.Errorf("%s = %q, want the control characters taken out", field.name, field.value)
		}
	}

	// A forged line would be a line that starts with the text the reply put after
	// its newline; a real line starts with the time of the run instead.
	forgedLine := "2026-01-01 00:00:00 [ERROR] " + forge
	for _, line := range strings.Split(readLog(t, dir), "\n") {
		if strings.HasPrefix(line, forgedLine) {
			t.Errorf("a reply field forged a log line: %s", line)
		}
	}
}

// The online_list request already refuses to follow a redirect, since a captive
// portal interception answers with one. The login needs the same defense for a
// second reason: the password is in the URL the redirect names.
func TestALoginRedirectIsNotFollowed(t *testing.T) {
	var elsewhere int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&elsewhere, 1)
		_, _ = io.WriteString(w, `{"result":1,"ret_code":"0"}`)
	}))
	defer other.Close()

	var logins int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, portalHome)
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("a") != "login" {
			_, _ = io.WriteString(w, `{"result":0}`)
			return
		}
		atomic.AddInt32(&logins, 1)
		http.Redirect(w, r, other.URL+"/eportal/?a=login", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := NewClient(testConfig(srv.URL)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	if res.Success {
		t.Fatalf("Login = %+v, want the redirect refused", res)
	}
	if got := atomic.LoadInt32(&logins); got != 1 {
		t.Errorf("the portal saw %d logins, want the one request", got)
	}
	if got := atomic.LoadInt32(&elsewhere); got != 0 {
		t.Errorf("the host the login was redirected to received %d requests, want none: the password must not be carried there", got)
	}
	if !strings.Contains(res.Message, "302") {
		t.Errorf("Message = %q, want the redirect reported by its status", res.Message)
	}
}

// The real portal sends a browser on to a page of its own, so a same-host
// redirect has to stay followable: refusing them all would stop the client from
// finding the address it logs in with.
func TestTheHomePageFollowsASameHostRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login.jsp", http.StatusFound)
	})
	mux.HandleFunc("/login.jsp", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, portalHome)
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"result":0}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st := NewClient(testConfig(srv.URL)).QueryStatus()

	if st.ClientIP != "203.0.113.7" {
		t.Errorf("ClientIP = %q, want the address from the page the portal sent us to", st.ClientIP)
	}
	if !st.Known {
		t.Errorf("Status = %+v, want a same-host redirect to be followed", st)
	}
}

// A page that sends the client to another host is what a hijacked answer looks
// like. The address on the page it lands on would be adopted as this client's and
// used for the next request, and the fetch itself goes to a host the
// configuration never named.
func TestTheHomePageDoesNotFollowACrossHostRedirect(t *testing.T) {
	var elsewhere int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&elsewhere, 1)
		_, _ = io.WriteString(w, portalHome)
	}))
	defer other.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/", http.StatusFound)
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"result":0}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// The two loopback names, because the check compares host names: the portal is
	// read under one and the redirect names the other, while both answer on this
	// machine so a followed hop would be counted.
	st := NewClient(testConfig(loopbackAlias(srv.URL))).QueryStatus()

	if got := atomic.LoadInt32(&elsewhere); got != 0 {
		t.Errorf("the host the page redirected to received %d requests, want none", got)
	}
	if st.ClientIP != "" {
		t.Errorf("ClientIP = %q, want the redirected page's address not to be adopted", st.ClientIP)
	}
	if st.Known {
		t.Errorf("Status = %+v, want an offline session with no address to be unknown", st)
	}
	if !strings.Contains(st.Message, "client address") {
		t.Errorf("Message = %q, want the missing address named", st.Message)
	}
}

// The hops are bounded as well: a chain of redirects is a request amplifier even
// when every hop stays on the same host.
func TestTheHomePageStopsAfterAHandfulOfRedirects(t *testing.T) {
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Redirect(w, r, "/hop"+r.URL.Path, http.StatusFound)
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"result":0}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st := NewClient(testConfig(srv.URL)).QueryStatus()

	if got := atomic.LoadInt32(&hits); got != homeRedirectLimit {
		t.Errorf("the home page took %d requests, want the cap of %d", got, homeRedirectLimit)
	}
	if st.ClientIP != "" {
		t.Errorf("ClientIP = %q, want no address after a chain of redirects", st.ClientIP)
	}
	if st.Known {
		t.Errorf("Status = %+v, want an offline session with no address to be unknown", st)
	}
}

// The address on the page is text, and the host that serves the page has proved
// nothing. A value that is not an address is dropped rather than handed to the
// next request as wlan_user_ip.
func TestAnAddressThatIsNotAnAddressIsRefused(t *testing.T) {
	t.Run("from the page", func(t *testing.T) {
		srv := stubEndpoints(t, `<script>var v4ip = '999.999.999.999';</script>`, `{"result":0}`)

		st := NewClient(testConfig(srv.URL)).QueryStatus()

		if st.ClientIP != "" {
			t.Errorf("ClientIP = %q, want a value that is not an address to be dropped", st.ClientIP)
		}
	})

	t.Run("from the reply", func(t *testing.T) {
		// The reply names the authenticated address and it wins over the page, but
		// only when it is an address: the page's own value is kept here.
		srv := stubEndpoints(t, portalHome, `{"result":1,"online_ip":"999.999.999.999"}`)

		st := NewClient(testConfig(srv.URL)).QueryStatus()

		if st.ClientIP != "203.0.113.7" {
			t.Errorf("ClientIP = %q, want the address from the page kept", st.ClientIP)
		}
	})
}

// A login cannot be attributed to a client without an address, and a page that
// cannot name the client is also what a forged page looks like. An offline
// session with no address is reported as unknown, so nothing is submitted and the
// state is visible instead.
func TestAnOfflineSessionWithoutAClientAddressIsUnknown(t *testing.T) {
	srv := stubEndpoints(t, "<html>a page with no address in it</html>", `{"result":0}`)

	st := NewClient(testConfig(srv.URL)).QueryStatus()

	if st.Known {
		t.Errorf("Status = %+v, want the state to be unknown rather than offline", st)
	}
	if !strings.Contains(st.Message, "client address") {
		t.Errorf("Message = %q, want the missing address named", st.Message)
	}
}

// The same page on an online session is still readable: the address is only
// needed to log in, and there is nothing to log in to.
func TestAnOnlineSessionWithoutAPageAddressIsStillKnown(t *testing.T) {
	srv := stubEndpoints(t, "<html>a page with no address in it</html>",
		`{"result":1,"online_ip":"203.0.113.9","user_account":"s1234567"}`)

	st := NewClient(testConfig(srv.URL)).QueryStatus()

	if !st.Known || !st.Online {
		t.Fatalf("Status = %+v, want the reply to be usable", st)
	}
	if st.ClientIP != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want the address from the reply", st.ClientIP)
	}
}

// The address a caller passes to Login is checked as well, since the request
// carries it as wlan_user_ip: a value of its own choosing is refused rather than
// sent, while an empty one is still submitted, which is the behavior the campus
// portal is used with.
func TestLoginWithAnUnusableClientAddressIsNotSent(t *testing.T) {
	s := &stubPortal{loginReply: `{"result":1,"ret_code":"0"}`}
	client := NewClient(s.start(t))

	res := client.Login(Status{ClientIP: "999.999.999.999"}, testAttempt())

	if res.Success {
		t.Errorf("Login = %+v, want it refused", res)
	}
	if !strings.Contains(res.Message, "not an IP address") {
		t.Errorf("Message = %q, want the reason", res.Message)
	}
	if s.lastLoginQuery() != nil {
		t.Error("the login reached the portal with an address that is not one")
	}

	res = client.Login(Status{}, testAttempt())
	if q := s.lastLoginQuery(); q == nil {
		t.Fatal("a login with no client address was not sent")
	} else if got := q.Get("wlan_user_ip"); got != "" {
		t.Errorf("wlan_user_ip = %q, want it empty", got)
	}
	if !res.Success {
		t.Errorf("Login = %+v, want the portal's answer", res)
	}
}

// A portal that fails an authentication quotes what it was given, and it does not
// have to name the parameter: MaskSecrets matches the name user_password alone,
// so the values of the whole login list are masked as well.
func TestAPortalReplyThatEchoesAPasswordIsMasked(t *testing.T) {
	s := &stubPortal{loginReply: `{"result":0,"ret_code":"99","msg":"authentication failed: password p@ss is wrong, and other-secret is too"}`}
	cfg := s.start(t)
	cfg.Accounts = []config.Account{
		{Username: "s1234567", Password: "p@ss", ISPs: []string{"@zgyd"}},
		{Username: "s7654321", Password: "other-secret", ISPs: []string{"@zgyd"}},
	}

	res := NewClient(cfg).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	for _, secret := range []string{"p@ss", "other-secret"} {
		if strings.Contains(res.Message, secret) {
			t.Errorf("Message = %q, want %q taken out", res.Message, secret)
		}
	}
	if !strings.Contains(res.Message, "***") {
		t.Errorf("Message = %q, want the value replaced", res.Message)
	}
}

// The reply kept for the settings page carries the same echo, and a query string
// escapes what it carries, so both spellings are taken out of it.
func TestTheKeptReplyMasksAnEscapedEchoOfThePassword(t *testing.T) {
	s := &stubPortal{loginReply: `{"result":0,"ret_code":"99","echo":"password p%40ss was refused"}`}

	res := NewClient(s.start(t)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

	for _, secret := range []string{"p@ss", "p%40ss"} {
		if strings.Contains(res.Raw, secret) {
			t.Errorf("Raw = %q, want %q taken out", res.Raw, secret)
		}
	}
	if !strings.Contains(res.Raw, "***") {
		t.Errorf("Raw = %q, want the value replaced", res.Raw)
	}
}

// online_list is judged by its http status and the login was not: a reply the
// portal never made, with "result":1 in it, was read as an accepted login. The
// two are judged the same way now, and nothing is read out of such a reply.
func TestALoginReplyWithAnErrorStatusIsNotAnAnswer(t *testing.T) {
	for _, code := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusFound} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			s := &stubPortal{loginReply: `{"result":1,"ret_code":"0"}`, loginStatus: code}

			res := NewClient(s.start(t)).Login(Status{ClientIP: "203.0.113.7"}, testAttempt())

			if res.Success {
				t.Fatalf("Login = %+v, want http %d refused", res, code)
			}
			if res.RetCode != "" {
				t.Errorf("RetCode = %q, want nothing read out of a reply that is not an answer", res.RetCode)
			}
			if !strings.Contains(res.Message, strconv.Itoa(code)) {
				t.Errorf("Message = %q, want the status in it", res.Message)
			}
		})
	}
}

// loopbackAlias is the same address under its other name, so a test can build two
// hosts that are told apart by name and still reached on this machine.
func loopbackAlias(raw string) string {
	return strings.Replace(raw, "127.0.0.1", "localhost", 1)
}

// The shipped configuration reads the portal page on one port and logs in on
// another, so a redirect that only changes the port is the same host and stays
// followable. Refusing it would stop the client from finding v4ip on a portal
// that sends the browser to its own service port.
func TestTheHomePageFollowsARedirectThatOnlyChangesThePort(t *testing.T) {
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, portalHome)
	}))
	defer second.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL+"/login.jsp", http.StatusFound)
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"result":0}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st := NewClient(testConfig(srv.URL)).QueryStatus()

	if st.ClientIP != "203.0.113.7" {
		t.Errorf("ClientIP = %q, want the address from the page on the other port", st.ClientIP)
	}
}
