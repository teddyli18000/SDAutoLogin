package portal

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"sdautologin/internal/config"
)

const portalHome = `<html><script>var v4ip = '10.90.0.85'; var v6ip = '2001:db8::1';</script></html>`

// testConfig points both portal endpoints at one stub server.
func testConfig(base string) *config.Config {
	return &config.Config{
		Username:           "23D001",
		Password:           "p@ss",
		AccountPrefix:      ",0,",
		AccountSuffix:      "@zgyd",
		PortalBaseURL:      base + "/",
		EportalURL:         base + "/eportal/",
		HTTPTimeoutSeconds: 5,
		JsVersion:          "3.3.3",
	}
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
	s := &stubPortal{onlineList: `{"result":1,"user_account":"23D001","online_ip":"10.90.0.99",` +
		`"online_mac":"d8ef42ae2054","online_time":"2026-09-14 08:56:10","online_ipv6_1":"2001:db8::2"}`}

	st := NewClient(s.start(t)).QueryStatus()

	if !st.Reachable || !st.Known || !st.Online {
		t.Fatalf("Status = %+v, want a reachable online session", st)
	}
	// The address online_list reports is the authenticated one and wins over the
	// one the home page embeds.
	if st.ClientIP != "10.90.0.99" {
		t.Errorf("ClientIP = %q, want the address online_list reported", st.ClientIP)
	}
	if st.ClientIPv6 != "2001:db8::2" {
		t.Errorf("ClientIPv6 = %q", st.ClientIPv6)
	}
	if st.Account != "23D001" || st.MAC != "d8ef42ae2054" || st.OnlineSince != "2026-09-14 08:56:10" {
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

	if st.ClientIP != "10.90.0.85" {
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

	res := NewClient(s.start(t)).Login(Status{ClientIP: "10.90.0.85"})

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

	res := NewClient(s.start(t)).Login(Status{ClientIP: "10.90.0.85"})

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

	res := NewClient(s.start(t)).Login(Status{ClientIP: "10.90.0.85"})

	if want := "\u7528\u6237\u4e0d\u5b58\u5728"; res.Message != want {
		t.Errorf("Message = %q, want %q", res.Message, want)
	}
}

func TestLoginSendsTheExpectedParameters(t *testing.T) {
	s := &stubPortal{loginReply: `{"result":1,"ret_code":"0"}`}

	NewClient(s.start(t)).Login(Status{
		ClientIP:   "10.90.0.85",
		ClientIPv6: "2001:db8::1",
		MAC:        "d8ef42ae2054",
	})

	q := s.lastLoginQuery()
	if q == nil {
		t.Fatal("no login request reached the portal")
	}
	want := map[string]string{
		"c":              "Portal",
		"a":              "login",
		"login_method":   "1",
		"user_account":   ",0,23D001@zgyd",
		"user_password":  "p@ss",
		"wlan_user_ip":   "10.90.0.85",
		"wlan_user_ipv6": "2001:db8::1",
		"wlan_user_mac":  "d8ef42ae2054",
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

	NewClient(s.start(t)).Login(Status{ClientIP: "10.90.0.85"})

	if got := s.lastLoginQuery().Get("wlan_user_mac"); got != "000000000000" {
		t.Errorf("wlan_user_mac = %q, want the zero mac", got)
	}
}

func TestLoginReportsAFailedRequest(t *testing.T) {
	res := NewClient(testConfig(closedBase(t))).Login(Status{ClientIP: "10.90.0.85"})

	if res.Success {
		t.Error("Success = true although the request could not be sent")
	}
	if !strings.Contains(res.Message, "request failed") {
		t.Errorf("Message = %q", res.Message)
	}
}

func TestTheClientNeverLogsOut(t *testing.T) {
	// A logout would leave the machine offline, which is exactly what this
	// program exists to prevent.
	s := &stubPortal{onlineList: `{"result":0}`, loginReply: `{"result":1,"ret_code":"0"}`}
	client := NewClient(s.start(t))

	st := client.QueryStatus()
	client.Login(st)
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

func TestLoggedURLsNeverContainThePassword(t *testing.T) {
	// The login request carries the password in its query string and that URL is
	// written to the log, so it has to be masked first.
	client := NewClient(&config.Config{Password: "p@ss"})
	raw := "http://portal/eportal/?a=login&user_password=" + url.QueryEscape("p@ss")

	got := client.safeURL(raw)
	if strings.Contains(got, url.QueryEscape("p@ss")) || strings.Contains(got, "p@ss") {
		t.Errorf("safeURL left the password in place: %s", got)
	}
	if !strings.Contains(got, "***") {
		t.Errorf("safeURL = %s, want the password replaced", got)
	}
}
