package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sdautologin/internal/config"
	"sdautologin/internal/logging"
)

// fakeAddr stands in for a listener address in the tests that only exercise the
// address formatting.
type fakeAddr struct{ s string }

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return a.s }

// startedServer returns a server listening on addr and the address it hands to
// a browser, which carries the token the session is opened with.
func startedServer(t *testing.T, addr string) (*Server, string) {
	t.Helper()

	state := New(&config.Config{Accounts: []config.Account{{Username: "s1234567", Password: "p@ss"}}})
	server := NewServer(state, addr, nil)
	t.Cleanup(server.Stop)

	url, err := server.URL()
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	return server, url
}

// browser is a client that keeps cookies, as a browser does between the address it
// was opened with and the page it is left on.
func browser(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

// pageAddress is the address of the server without the token in it, which is
// what the page is served at.
func pageAddress(url string) string {
	return url[:strings.Index(url, "/?")]
}

func TestServerServesThePageOnTheAddressItWasGiven(t *testing.T) {
	_, url := startedServer(t, "127.0.0.1:0")

	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Errorf("url = %q, want a loopback address", url)
	}
	if !strings.Contains(url, "?token=") {
		t.Errorf("url = %q, want the token in it", url)
	}
}

func TestServerRefusesARequestWithoutTheToken(t *testing.T) {
	// The session cookie is all that stands between the page and any other
	// program that can reach the address, so it has to be required.
	_, url := startedServer(t, "127.0.0.1:0")

	allowed, err := browser(t).Get(url)
	if err != nil {
		t.Fatalf("get the address the tray handed out: %v", err)
	}
	defer allowed.Body.Close()
	if allowed.StatusCode != http.StatusOK {
		t.Errorf("opening the address: status %d, want 200", allowed.StatusCode)
	}

	refused, err := http.Get(pageAddress(url) + "/api/status")
	if err != nil {
		t.Fatalf("get without the cookie: %v", err)
	}
	defer refused.Body.Close()
	if refused.StatusCode != http.StatusForbidden {
		t.Errorf("without the cookie: status %d, want 403", refused.StatusCode)
	}
}

// TestThePageFilesNeedTheSessionToo covers what the page loads for itself. The
// script, the stylesheet and the mark are answered through the same check as the
// rest of the server: a program that found the port without being handed the
// address reads no part of the page, and the browser that did open the address
// is served all of it.
func TestThePageFilesNeedTheSessionToo(t *testing.T) {
	_, url := startedServer(t, "127.0.0.1:0")
	base := pageAddress(url)
	files := []string{"/static/app.js", "/static/app.css", "/static/logo.svg"}

	for _, name := range files {
		refused, err := http.Get(base + name)
		if err != nil {
			t.Fatalf("get %s without the cookie: %v", name, err)
		}
		refused.Body.Close()
		if refused.StatusCode != http.StatusForbidden {
			t.Errorf("%s without the cookie: status %d, want 403", name, refused.StatusCode)
		}
	}

	// The browser follows the address through to the page, which is what leaves
	// it holding the cookie the files are read with.
	client := browser(t)
	page, err := client.Get(url)
	if err != nil {
		t.Fatalf("open the address: %v", err)
	}
	page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("opening the address: status %d, want 200", page.StatusCode)
	}
	for _, name := range files {
		served, err := client.Get(base + name)
		if err != nil {
			t.Fatalf("get %s with the cookie: %v", name, err)
		}
		served.Body.Close()
		if served.StatusCode != http.StatusOK {
			t.Errorf("%s with the cookie: status %d, want 200", name, served.StatusCode)
		}
	}
}

// TestTheAddressIsExchangedForASessionCookie covers what happens to the token the
// tray hands out: the address bar, the history and the page's Referer are the same
// string, so the token is taken out of it as soon as the page is served.
func TestTheAddressIsExchangedForASessionCookie(t *testing.T) {
	server, url := startedServer(t, "127.0.0.1:0")

	client := browser(t)
	// The redirect is left alone, so the test reads the reply that carries the cookie.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("get the address: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want a redirect to the page without the token", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/" {
		t.Errorf("Location = %q, want /", got)
	}

	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want exactly one", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookie {
		t.Errorf("cookie name = %q, want %q", cookie.Name, sessionCookie)
	}
	if cookie.Path != "/" {
		t.Errorf("cookie path = %q, want /", cookie.Path)
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie SameSite = %v, want Strict, which keeps it out of a request from another site", cookie.SameSite)
	}
	if cookie.Value != server.session {
		t.Errorf("cookie value = %q, want the token the page goes on using", cookie.Value)
	}
	// The token the address carried is not the one the page keeps, so an address
	// that was seen is not the session.
	used := url[strings.Index(url, "token=")+len("token="):]
	if cookie.Value == used {
		t.Errorf("the address and the session carry the same token %q, so an address that was seen stays a way in", used)
	}
}

// TestTheAddressIsGoodForOneOpening covers what one-opening addresses are for:
// what a browser was left with, and what someone read out of a log or a history,
// is not a way in a second time. The server makes the next one as it spends this.
func TestTheAddressIsGoodForOneOpening(t *testing.T) {
	server, url := startedServer(t, "127.0.0.1:0")

	first, err := browser(t).Get(url)
	if err != nil {
		t.Fatalf("first opening: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first opening: status %d, want 200", first.StatusCode)
	}

	second, err := browser(t).Get(url)
	if err != nil {
		t.Fatalf("second opening: %v", err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusForbidden {
		t.Errorf("second opening: status %d, want 403, the address was spent by the first", second.StatusCode)
	}

	next, err := server.URL()
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if next == url {
		t.Fatal("the spent address was not replaced")
	}
	opened, err := browser(t).Get(next)
	if err != nil {
		t.Fatalf("opening the next address: %v", err)
	}
	opened.Body.Close()
	if opened.StatusCode != http.StatusOK {
		t.Errorf("opening the next address: status %d, want 200", opened.StatusCode)
	}
}

// TestEveryOpeningGetsItsOwnAddress is the tray path: two openings in a row have
// to work, so the second cannot be handed the address the first used.
func TestEveryOpeningGetsItsOwnAddress(t *testing.T) {
	server, _ := startedServer(t, "127.0.0.1:0")

	handed := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		url, err := server.open(func(string) error { return nil })
		if err != nil {
			t.Fatalf("open %d: %v", i+1, err)
		}
		for _, earlier := range handed {
			if url == earlier {
				t.Fatalf("opening %d was handed %q, the address of the opening before it", i+1, url)
			}
		}
		handed = append(handed, url)

		resp, err := browser(t).Get(url)
		if err != nil {
			t.Fatalf("opening %d: %v", i+1, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("opening %d: status %d, want 200", i+1, resp.StatusCode)
		}
	}
}

func TestTheTokenInAnAddressIsNotAcceptedOnTheApi(t *testing.T) {
	// Only the first navigation the browser makes on its own can put the token
	// in an address, so no other endpoint reads it from there. What a caller would
	// otherwise copy out of a log or a history is not a way to the settings.
	server, url := startedServer(t, "127.0.0.1:0")
	base := pageAddress(url)

	for _, c := range []struct{ name, token string }{
		{"the live session token", server.session},
		{"the token of the address", server.entry},
	} {
		resp, err := http.Get(base + "/api/status?token=" + c.token)
		if err != nil {
			t.Fatalf("get with %s in the address: %v", c.name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s in the address: status %d, want 403", c.name, resp.StatusCode)
		}
	}
}

func TestAWriteNeedsTheHeaderAsWellAsTheCookie(t *testing.T) {
	// A page from another site cannot set a custom header without this server
	// agreeing to a cross site request, so a write that carries the cookie alone is
	// refused as well.
	server, url := startedServer(t, "127.0.0.1:0")
	base := pageAddress(url)

	for _, c := range []struct {
		name   string
		header bool
		want   int
	}{
		{"with the cookie alone", false, http.StatusForbidden},
		{"with the header as well", true, http.StatusOK},
	} {
		req, err := http.NewRequest(http.MethodPost, base+"/api/action",
			strings.NewReader(`{"action":"check"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: server.session})
		if c.header {
			req.Header.Set("X-Auth-Token", server.session)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s: status %d, want %d", c.name, resp.StatusCode, c.want)
		}
	}
}

// TestTheExitButtonReachesTheCallbackThatWasSetLast covers the run that took over
// the tray's server: the callback the tray gave it posts to a window that never
// got a message loop, so the fallback replaces it with one that ends this run, and
// the page's exit button must reach the new one.
func TestTheExitButtonReachesTheCallbackThatWasSetLast(t *testing.T) {
	state := New(&config.Config{Accounts: []config.Account{{Username: "s1234567", Password: "p@ss"}}})

	trayQuit := make(chan struct{}, 1)
	server := NewServer(state, "127.0.0.1:0", func() { trayQuit <- struct{}{} })
	t.Cleanup(server.Stop)

	fallbackQuit := make(chan struct{}, 1)
	server.SetOnQuit(func() { fallbackQuit <- struct{}{} })

	url, err := server.URL()
	if err != nil {
		t.Fatalf("URL: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, pageAddress(url)+"/api/action",
		strings.NewReader(`{"action":"quit"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: server.session})
	req.Header.Set("X-Auth-Token", server.session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("quit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("quit: status %d, want 200", resp.StatusCode)
	}

	// The callback is called on a goroutine of its own, so the reply arriving
	// first does not promise that it has run yet.
	select {
	case <-fallbackQuit:
	case <-time.After(5 * time.Second):
		t.Fatal("the exit button did not reach the callback the fallback set")
	}
	select {
	case <-trayQuit:
		t.Error("the exit button posted to the window the tray would have used")
	default:
	}
}

// TestTheServerBoundsEveryPhaseOfARequest holds the server to a deadline on
// every step of a request. A form the page sends arrives in one piece, so a
// caller that sends one byte and then stops is not someone to wait for.
func TestTheServerBoundsEveryPhaseOfARequest(t *testing.T) {
	server := NewServer(New(&config.Config{}), "", nil)
	t.Cleanup(server.Stop)
	if _, err := server.URL(); err != nil {
		t.Fatalf("URL: %v", err)
	}

	for _, c := range []struct {
		name string
		d    time.Duration
	}{
		{"reading the request header", server.srv.ReadHeaderTimeout},
		{"reading the request", server.srv.ReadTimeout},
		{"writing the reply", server.srv.WriteTimeout},
		{"keeping an idle connection", server.srv.IdleTimeout},
	} {
		if c.d <= 0 {
			t.Errorf("%s has no deadline", c.name)
		}
	}
}

func TestTheReplyDeadlineFollowsTheRequestTimeout(t *testing.T) {
	// A handler may wait for the portal on each request it makes, so the reply
	// deadline has to sit above what the configuration allows, or the page would
	// be told the request failed while the program was still waiting for it.
	short := writeTimeout(&config.Config{HTTPTimeoutSeconds: 2})
	if short < settingsMinWriteTimeout {
		t.Errorf("writeTimeout(2s) = %v, want at least %v", short, settingsMinWriteTimeout)
	}

	long := writeTimeout(&config.Config{HTTPTimeoutSeconds: 120})
	if long < 120*time.Second*settingsPortalRequests {
		t.Errorf("writeTimeout(120s) = %v, too short for %d requests of 120s",
			long, settingsPortalRequests)
	}
	if long <= short {
		t.Errorf("writeTimeout(120s) = %v, want more than the one for 2s (%v)", long, short)
	}
}

// deadlineWriter is the part of a connection the reply deadline is set on. A real
// one is the socket net/http wraps; this records what a handler asked for.
type deadlineWriter struct {
	http.ResponseWriter
	deadline time.Time
}

func (w *deadlineWriter) SetWriteDeadline(t time.Time) error {
	w.deadline = t
	return nil
}

// deadlineFor runs one request through the server's handler chain and returns the
// deadline the reply was given, counted from just before the call.
func deadlineFor(t *testing.T, server *Server, target string) time.Duration {
	t.Helper()

	rec := &deadlineWriter{ResponseWriter: httptest.NewRecorder()}
	start := time.Now()
	server.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.deadline.IsZero() {
		t.Fatalf("%s was answered without a reply deadline", target)
	}
	return rec.deadline.Sub(start)
}

// TestTheReplyDeadlineIsSetForEveryKindOfAnswer covers which answers carry the
// deadline: the whole chain is behind it, so a refusal and a missing address are
// bounded like a page is. A caller that stops reading holds its connection open
// either way.
func TestTheReplyDeadlineIsSetForEveryKindOfAnswer(t *testing.T) {
	state := New(&config.Config{HTTPTimeoutSeconds: 6})
	server := NewServer(state, "", nil)
	t.Cleanup(server.Stop)

	want := writeTimeout(state.Config())
	for _, target := range []string{
		"/",              // a refusal: no token, no cookie
		"/static/app.js", // the page's own files, refused the same way
		"/api/status",    // the api, refused the same way
		"/nowhere",       // nothing is served there
	} {
		if got := deadlineFor(t, server, target); got < want-time.Second || got > want+time.Second {
			t.Errorf("%s was given %v, want about %v", target, got, want)
		}
	}
}

// TestTheReplyDeadlineFollowsAChangeOfConfiguration is the reason the deadline is
// set per request: the request timeout is a setting on the page, and the server
// outlives a save. A copy that kept the deadline it started with would cut off a
// reply the reader had just asked to be allowed to wait for.
func TestTheReplyDeadlineFollowsAChangeOfConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.HTTPTimeoutSeconds = 2
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	state := New(cfg)
	server := NewServer(state, "", nil)
	t.Cleanup(server.Stop)

	before := writeTimeout(state.Config())
	if got := deadlineFor(t, server, "/"); got < before-time.Second || got > before+time.Second {
		t.Fatalf("the deadline before the change was %v, want about %v", got, before)
	}

	// The same save the page makes, on the same state the server reads from.
	next := cfg.Clone()
	next.HTTPTimeoutSeconds = 300
	if err := state.Update(next); err != nil {
		t.Fatal(err)
	}

	after := writeTimeout(state.Config())
	if after <= before {
		t.Fatalf("writeTimeout after the save = %v, want more than %v", after, before)
	}
	if got := deadlineFor(t, server, "/"); got < after-time.Second || got > after+time.Second {
		t.Errorf("the deadline after the change was %v, want about %v: the server is still using the configuration it started with", got, after)
	}
}

func TestEveryAnswerCarriesTheSecurityHeaders(t *testing.T) {
	// The page is opened with the token in its address, so an answer must keep that
	// address out of a Referer, must not be framed by another page, and must name
	// what it may load from its own policy. The refusal a caller without the token
	// reads carries them too.
	_, url := startedServer(t, "127.0.0.1:0")

	// One client that has been through the exchange and holds the cookie, since
	// the page is only served to one of those.
	opened := browser(t)
	if resp, err := opened.Get(url); err != nil {
		t.Fatalf("open the address: %v", err)
	} else {
		resp.Body.Close()
	}

	for _, c := range []struct {
		name   string
		client *http.Client
		target string
	}{
		{"the exchange", http.DefaultClient, url},
		{"the page", opened, pageAddress(url)},
		{"a refusal", http.DefaultClient, pageAddress(url) + "/api/status"},
	} {
		resp, err := c.client.Get(c.target)
		if err != nil {
			t.Fatalf("get %s: %v", c.name, err)
		}
		resp.Body.Close()

		want := map[string]string{
			"Content-Security-Policy": contentSecurityPolicy,
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
		}
		for header, value := range want {
			if got := resp.Header.Get(header); got != value {
				t.Errorf("%s: %s = %q, want %q", c.name, header, got, value)
			}
		}
	}

	// The rule the policy is built on: the page has no inline script and no
	// inline style, so allowing either would only leave an injected one a place
	// to run.
	if strings.Contains(contentSecurityPolicy, "unsafe-inline") {
		t.Errorf("the policy allows inline code: %s", contentSecurityPolicy)
	}
}

func TestAMissingTokenIsOneNothingMatches(t *testing.T) {
	// The comparison is a constant time one and an empty pair matches, so a token
	// that was never made is refused before the comparison runs: a server that was
	// never started must not accept a caller who sends no token.
	if tokenEqual("", "") {
		t.Error("an empty token matched a token that was never made")
	}
	if tokenEqual("anything", "") {
		t.Error("a value matched a token that was never made")
	}
	if !tokenEqual("tok", "tok") {
		t.Error("equal tokens did not match")
	}
	if tokenEqual("tok", "token") {
		t.Error("tokens of different lengths matched")
	}

	server := NewServer(New(&config.Config{}), "", nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: ""})
	if server.authorised(req) {
		t.Error("a server that was never started authorised a request")
	}
}

func TestURLForPicksAnAddressABrowserCanUse(t *testing.T) {
	cases := []struct {
		bound string
		want  string
	}{
		{"127.0.0.1:8088", "http://127.0.0.1:8088/?token=tok"},
		// A listener on every interface has no host of its own.
		{"0.0.0.0:8088", "http://127.0.0.1:8088/?token=tok"},
		{"[::]:8088", "http://127.0.0.1:8088/?token=tok"},
		// An address that was asked for is kept.
		{"192.168.1.5:8088", "http://192.168.1.5:8088/?token=tok"},
		{"[fe80::1]:8088", "http://[fe80::1]:8088/?token=tok"},
	}

	for _, c := range cases {
		if got := urlFor(fakeAddr{c.bound}, "tok"); got != c.want {
			t.Errorf("urlFor(%q) = %q, want %q", c.bound, got, c.want)
		}
	}
}

// TestARefusedSaveLeavesTheRunningConfigAlone covers a refused request: the page
// posts the whole configuration back, so the running one comes out of it untouched,
// or a mistyped password takes the session down while the page reports the refusal.
func TestARefusedSaveLeavesTheRunningConfigAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	// A file in the shape the version with one account wrote, so the test also
	// covers the upgrade of an installation that is already there.
	older := `{"username":"s1234567","password":"topsecret","accountSuffix":"@zgyd",` +
		`"portalBaseUrl":"http://10.9.1.3/","eportalUrl":"http://10.9.1.3:801/eportal/",` +
		`"accountPrefix":",0,","checkIntervalSeconds":15,"httpTimeoutSeconds":6}`
	if err := os.WriteFile(path, []byte(older), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	state := New(cfg)
	server := NewServer(state, "", nil)
	t.Cleanup(server.Stop)
	if _, err := server.URL(); err != nil {
		t.Fatalf("URL: %v", err)
	}

	// The account name is what is wrong with this request. An empty password is
	// not wrong by itself: that is how the page says "keep the one that is stored",
	// and an entry with no name has nothing to keep a password for.
	body := `{"accounts":[{"username":"","password":"","isps":["@cucc"]}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader([]byte(body)))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: server.session})
	req.Header.Set("X-Auth-Token", server.session)
	rec := httptest.NewRecorder()
	server.handleConfig(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "err.accountRequired") {
		t.Errorf("the reply does not name the account: %s", rec.Body.String())
	}

	live := state.Config().Accounts
	if len(live) != 1 {
		t.Fatalf("Accounts = %+v, want the one entry of the file", live)
	}
	if live[0].Password != "topsecret" {
		t.Errorf("the running config was left with password %q", live[0].Password)
	}
	if len(live[0].ISPs) != 1 || live[0].ISPs[0] != "@zgyd" {
		t.Errorf("the running config was left with isps %v", live[0].ISPs)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != older {
		t.Errorf("the file was written by a request that was refused:\n%s", stored)
	}
}

// pageOf starts a settings server on a configuration of a test's own and hands
// back what its page is given when it asks for the configuration.
func pageOf(t *testing.T, cfg *config.Config) (*Server, string) {
	t.Helper()

	state := New(cfg)
	server := NewServer(state, "", nil)
	t.Cleanup(server.Stop)
	if _, err := server.URL(); err != nil {
		t.Fatalf("URL: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: server.session})
	req.Header.Set("X-Auth-Token", server.session)
	rec := httptest.NewRecorder()
	server.handleBootstrap(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	return server, rec.Body.String()
}

// The page writes the password field and never reads it, so a stored secret is not
// part of what it is given: it is told that there is one. A password that never
// leaves the process cannot be read back out of the browser.
func TestThePageIsNotGivenTheStoredPassword(t *testing.T) {
	cfg := &config.Config{
		Accounts:         []config.Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}},
		RememberPassword: true,
	}

	_, body := pageOf(t, cfg)
	if strings.Contains(body, "topsecret") {
		t.Errorf("the stored password was handed to the page: %s", body)
	}
	if !strings.Contains(body, `"passwordSource":"config"`) {
		t.Errorf("the page is not told where the password is: %s", body)
	}
}

// TestReloadingDoesNotHandThePasswordToThePage covers the second way the
// configuration comes back to the page: reload reads the file again, so it is the
// file's contents, and it is masked like the copy the page was opened with.
func TestReloadingDoesNotHandThePasswordToThePage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []config.Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	state := New(cfg)
	server := NewServer(state, "", nil)
	t.Cleanup(server.Stop)
	if _, err := server.URL(); err != nil {
		t.Fatalf("URL: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/action",
		bytes.NewReader([]byte(`{"action":"reload"}`)))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: server.session})
	req.Header.Set("X-Auth-Token", server.session)
	rec := httptest.NewRecorder()
	server.handleAction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("reload: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "topsecret") {
		t.Errorf("the reloaded configuration went back to the page with its password: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"passwordSource":"config"`) {
		t.Errorf("the page is not told where the password is: %s", rec.Body.String())
	}
}

// TestThePageIsToldNothingIsStoredWhenThePasswordsAreNotRemembered: with the
// passwords switched off there is nothing in the file for an empty field to keep,
// so the page saying otherwise is a promise the next start does not keep.
func TestThePageIsToldNothingIsStoredWhenThePasswordsAreNotRemembered(t *testing.T) {
	cfg := &config.Config{
		Accounts:         []config.Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}},
		RememberPassword: false,
	}

	_, body := pageOf(t, cfg)
	if strings.Contains(body, "topsecret") {
		t.Errorf("the stored password was handed to the page: %s", body)
	}
	if strings.Contains(body, `"passwordSource"`) {
		t.Errorf("the page was told where a password is, and the file holds none: %s", body)
	}
}

func TestASaveThatLeavesThePasswordAloneKeepsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []config.Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	state := New(cfg)
	server := NewServer(state, "", nil)
	t.Cleanup(server.Stop)
	if _, err := server.URL(); err != nil {
		t.Fatalf("URL: %v", err)
	}

	// What the page sends for an entry whose password field was left empty: the
	// account and the service type, and no password.
	body := `{"accounts":[{"username":"s1234567","isps":["@cucc"]}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader([]byte(body)))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: server.session})
	req.Header.Set("X-Auth-Token", server.session)
	rec := httptest.NewRecorder()
	server.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	live := state.Config().Accounts
	if len(live) != 1 || live[0].Password != "topsecret" {
		t.Fatalf("Accounts = %+v, want the stored password to have been kept", live)
	}
	if len(live[0].ISPs) != 1 || live[0].ISPs[0] != "@cucc" {
		t.Errorf("the change the page made was lost: %v", live[0].ISPs)
	}
	if strings.Contains(rec.Body.String(), "topsecret") {
		t.Errorf("the saved configuration went back to the page with its password")
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), "topsecret") {
		t.Errorf("the file lost the password:\n%s", stored)
	}
}

// postAction runs one action the page can ask for and hands back the reply, so the
// test reads the contract between the page and the handler.
func postAction(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader([]byte(body)))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: server.session})
	req.Header.Set("X-Auth-Token", server.session)
	rec := httptest.NewRecorder()
	server.handleAction(rec, req)
	return rec
}

// TestThePageCanAskForTheServiceTypeTheSessionShouldBeOn covers the reply the
// controls on the page are rendered from: whether there was anything to do, and
// what the sign-in went out as. A session already on the service type asked for is
// answered with nothing to do, like one that is not up.
func TestThePageCanAskForTheServiceTypeTheSessionShouldBeOn(t *testing.T) {
	cases := []struct {
		name      string
		isps      []string
		session   string
		isp       string
		moved     bool
		submitted string
	}{
		{"the first service type, with the session on the campus network",
			[]string{"@zgyd", ""}, "s1234567", "@zgyd", true, ",0,s1234567@zgyd"},
		{"another service type of the entry",
			[]string{"@zgyd", "@cucc"}, "s1234567@zgyd", "@cucc", true, ",0,s1234567@cucc"},
		{"a service type the entry does not list",
			[]string{"@zgyd", "@cucc"}, "s1234567", "@ctc", false, ""},
		{"a session that is down",
			[]string{"@zgyd", "@cucc"}, offlineSession, "@cucc", false, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := newStubPortal(t, c.isps, c.session,
				`{"result":1,"ret_code":0,"msg":"authentication successful"}`)
			state := New(stub.cfg)
			server := NewServer(state, "", nil)
			t.Cleanup(server.Stop)
			if _, err := server.URL(); err != nil {
				t.Fatalf("URL: %v", err)
			}

			rec := postAction(t, server, `{"action":"switchService","isp":`+strconv.Quote(c.isp)+`}`)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			want := `"moved":false`
			if c.moved {
				want = `"moved":true`
			}
			if !strings.Contains(body, want) {
				t.Errorf("the reply does not say %s: %s", want, body)
			}
			if c.moved && !strings.Contains(body, `"isp":`+strconv.Quote(c.isp)) {
				t.Errorf("the reply does not name the service type the sign-in went out as: %s", body)
			}
			submitted := stub.accounts()
			if c.submitted == "" {
				if len(submitted) != 0 {
					t.Errorf("submitted = %q, want nothing with nothing to do", submitted)
				}
				return
			}
			if len(submitted) != 1 || submitted[0] != c.submitted {
				t.Errorf("submitted = %q, want %q", submitted, c.submitted)
			}
		})
	}
}

// TestASwitchWithoutAServiceTypeIsRefused covers the difference the request keeps:
// the campus network itself is a service type with no suffix, and a request naming
// no service type is malformed.
func TestASwitchWithoutAServiceTypeIsRefused(t *testing.T) {
	stub := newStubPortal(t, []string{"@zgyd", ""}, onlineSession, `{"result":1,"ret_code":0}`)
	state := New(stub.cfg)
	server := NewServer(state, "", nil)
	t.Cleanup(server.Stop)
	if _, err := server.URL(); err != nil {
		t.Fatalf("URL: %v", err)
	}

	rec := postAction(t, server, `{"action":"switchService"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if got := stub.loginCount(); got != 0 {
		t.Errorf("a login was submitted %d time(s) for a request that named no service type", got)
	}
}

// TestThePortalPageOpenedIsTheConfiguredOne covers the way out of a session the
// portal will not sign in again: the page asks for the portal's own page, and what
// reaches the desktop is the configured address. The address in the request is
// ignored, so no page can hand the shell one of its choosing.
func TestThePortalPageOpenedIsTheConfiguredOne(t *testing.T) {
	cfg := portalPageConfig()
	server, opened := portalPageServer(t, cfg, nil)

	rec := postAction(t, server, `{"action":"openPortal","url":"http://example.com/not-the-portal"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var reply struct {
		OK  bool   `json:"ok"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	if !reply.OK {
		t.Errorf("the reply is not ok: %s", rec.Body.String())
	}
	if reply.URL != cfg.PortalBaseURL {
		t.Errorf("the reply names %q, want the configured portal page %q", reply.URL, cfg.PortalBaseURL)
	}
	if len(*opened) != 1 || (*opened)[0] != cfg.PortalBaseURL {
		t.Errorf("opened %q, want exactly the configured portal page %q", *opened, cfg.PortalBaseURL)
	}
}

// TestAPortalPageThatCannotBeOpenedIsReported covers the other half of that
// button: when the desktop will not take the address, the request is answered as a
// failure with a message key, so the page says the button did nothing instead.
func TestAPortalPageThatCannotBeOpenedIsReported(t *testing.T) {
	server, opened := portalPageServer(t, portalPageConfig(), errors.New("nothing here takes http addresses"))

	rec := postAction(t, server, `{"action":"openPortal"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "err.openPortal") {
		t.Errorf("the reply does not name the message to show: %s", rec.Body.String())
	}
	if len(*opened) != 1 {
		t.Errorf("opened %q, want the one attempt that failed", *opened)
	}
}

// portalPageConfig is a configuration whose portal home page is known, which is
// the address the button beside a refusal is expected to open.
func portalPageConfig() *config.Config {
	return &config.Config{
		Accounts:      []config.Account{{Username: "s1234567", Password: "p@ss"}},
		PortalBaseURL: "http://10.9.1.3/",
	}
}

// portalPageServer is a running settings server whose desktop is replaced with
// one that records what it was asked to open, and fails when fail is set.
func portalPageServer(t *testing.T, cfg *config.Config, fail error) (*Server, *[]string) {
	t.Helper()

	server := NewServer(New(cfg), "", nil)
	t.Cleanup(server.Stop)
	if _, err := server.URL(); err != nil {
		t.Fatalf("URL: %v", err)
	}
	opened := &[]string{}
	server.openExternal = func(target string) error {
		*opened = append(*opened, target)
		return fail
	}
	return server, opened
}

// TestASaveCarriesThePreferredServiceSwitch covers the JSON name of that option
// on its way from the page: the page posts the whole configuration back, so a
// name the struct does not have would drop the switch in silence and the page
// would go on showing it as on.
func TestASaveCarriesThePreferredServiceSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	state := New(cfg)
	server := NewServer(state, "", nil)
	t.Cleanup(server.Stop)
	if _, err := server.URL(); err != nil {
		t.Fatalf("URL: %v", err)
	}

	body := `{"accounts":[{"username":"s1234567","password":"p@ss","isps":[""]}],` +
		`"switchToPreferredService":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader([]byte(body)))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: server.session})
	req.Header.Set("X-Auth-Token", server.session)
	rec := httptest.NewRecorder()
	server.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if state.Config().SwitchToPreferredService {
		t.Error("the running configuration still has the switch on")
	}
	// Read back from the file: the next start is what the switch has to hold for.
	reloaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SwitchToPreferredService {
		t.Error("the file still has the switch on, so the next start would move the session again")
	}
}

// TestAFirstSaveFromThePage covers the wizard's first save, when nothing is
// configured yet: the request carries the cards, so an account or a password left
// out of them is refused by name and the page shows the refusal beside its field.
func TestAFirstSaveFromThePage(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
		key  string
	}{
		{
			"an account with the password that was typed",
			`{"accounts":[{"username":"23D001","password":"hunter2","isps":[""]}]}`,
			http.StatusOK, "",
		},
		{
			"no account at all, the wizard's list before the account is put in it",
			`{"accounts":[]}`,
			http.StatusBadRequest, "err.accountsRequired",
		},
		{
			"an account whose password field came back empty",
			`{"accounts":[{"username":"23D001","isps":[""]}]}`,
			http.StatusBadRequest, "err.passwordRequired",
		},
		{
			"an account with no name",
			`{"accounts":[{"username":"","password":"hunter2","isps":[""]}]}`,
			http.StatusBadRequest, "err.accountRequired",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A configuration as a fresh installation has it: a file that exists
			// and holds no account, so there is no stored password for an empty
			// field to keep.
			cfg, err := config.LoadConfig(filepath.Join(t.TempDir(), "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			state := New(cfg)
			server := NewServer(state, "", nil)
			t.Cleanup(server.Stop)
			t.Cleanup(state.Stop)
			if _, err := server.URL(); err != nil {
				t.Fatalf("URL: %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader([]byte(c.body)))
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: server.session})
			req.Header.Set("X-Auth-Token", server.session)
			rec := httptest.NewRecorder()
			server.handleConfig(rec, req)

			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.want, rec.Body.String())
			}
			if c.key != "" && !strings.Contains(rec.Body.String(), c.key) {
				t.Errorf("the refusal does not name %s: %s", c.key, rec.Body.String())
			}
			if c.want == http.StatusOK {
				if got := state.Config().Accounts; len(got) != 1 || got[0].Password != "hunter2" {
					t.Errorf("Accounts = %+v, want the account with the password that was sent", got)
				}
				return
			}
			if got := len(state.Config().Accounts); got != 0 {
				t.Errorf("a refused first save left %d account(s) behind", got)
			}
		})
	}
}

// TestTheRefusalLineNamesEveryField covers the line a refused save writes to the
// log. It names every refused field in one order, so two refusals can be read
// against each other, and it carries no value.
func TestTheRefusalLineNamesEveryField(t *testing.T) {
	got := describeErrors(map[string]string{
		"accounts.1.password": "err.passwordRequired",
		"accounts":            "err.accountsRequired",
		"accounts.0.username": "err.accountRequired",
	})
	want := "accounts=err.accountsRequired; accounts.0.username=err.accountRequired; accounts.1.password=err.passwordRequired"
	if got != want {
		t.Errorf("describeErrors() = %q, want %q", got, want)
	}
	if got := describeErrors(nil); got != "" {
		t.Errorf("describeErrors(nil) = %q, want nothing", got)
	}
}

func TestIsLoopback(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "localhost", "LOCALHOST"} {
		if !isLoopback(host) {
			t.Errorf("isLoopback(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"", "0.0.0.0", "::", "192.168.1.5", "portal.example"} {
		if isLoopback(host) {
			t.Errorf("isLoopback(%q) = true, want false", host)
		}
	}
}

// captureStderr runs f with stderr sent into a pipe and returns what it wrote.
// The logger copies every line to the console as well as the file, so this is
// how a test sees what would be kept.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = saved
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return string(data)
}

// TestTheAddressWithoutTheTokenIsTheOneALogCanCarry holds that the server can
// name itself without the one time token: the log outlives the token and is
// copied around, so a line that carried it would be a way back in.
func TestTheAddressWithoutTheTokenIsTheOneALogCanCarry(t *testing.T) {
	server, url := startedServer(t, "127.0.0.1:0")

	addr, ok := server.Addr()
	if !ok {
		t.Fatal("Addr() reported no address for a server that is up")
	}
	if want := pageAddress(url); addr != want {
		t.Errorf("Addr() = %q, want %q", addr, want)
	}
	if strings.Contains(addr, "token=") {
		t.Errorf("Addr() = %q, want no token in it", addr)
	}
}

// TestNoLogLineCarriesTheToken drives every place the settings server names
// itself and holds that no line it writes carries the token.
func TestNoLogLineCarriesTheToken(t *testing.T) {
	logging.Log.Init(t.TempDir(), true)
	t.Cleanup(logging.Log.CloseFile)

	state := New(&config.Config{Accounts: []config.Account{{Username: "s1234567", Password: "p@ss"}}})
	// An empty address is the mode a tray uses: the page is served on loopback
	// when it is asked for, and nothing but the browser is told the address.
	server := NewServer(state, "", nil)

	var url string
	console := captureStderr(t, func() {
		var err error
		url, err = server.URL()
		if err != nil {
			t.Fatalf("URL: %v", err)
		}
		// A second opening rolls the entry token and names itself again.
		if _, err := server.rollEntry(); err != nil {
			t.Fatalf("rollEntry: %v", err)
		}
		server.Stop()
	})

	token := url[strings.Index(url, "token=")+len("token="):]
	if token == "" {
		t.Fatalf("url = %q, want the token in it", url)
	}
	if strings.Contains(console, token) {
		t.Errorf("a log line carries the token: %q", console)
	}
	assertLogFileLacks(t, token)
}

// TestTheRemoteManagementAddressGoesToTheConsoleNotTheLog covers the one mode
// where nothing opens the page on the reader's behalf: the machine has no tray,
// so the address has to be told to the person who asked for it. That is the
// console, which is deliberate, and never the log file, which outlives the token
// and is copied around.
func TestTheRemoteManagementAddressGoesToTheConsoleNotTheLog(t *testing.T) {
	logging.Log.Init(t.TempDir(), true)
	t.Cleanup(logging.Log.CloseFile)

	state := New(&config.Config{Accounts: []config.Account{{Username: "s1234567", Password: "p@ss"}}})
	server := NewServer(state, "127.0.0.1:0", nil)

	var url string
	console := captureStderr(t, func() {
		var err error
		url, err = server.URL()
		if err != nil {
			t.Fatalf("URL: %v", err)
		}
		server.Stop()
	})

	token := url[strings.Index(url, "token=")+len("token="):]
	if token == "" {
		t.Fatalf("url = %q, want the token in it", url)
	}
	if !strings.Contains(console, token) {
		t.Errorf("the console does not carry the address to open: %q", console)
	}
	assertLogFileLacks(t, token)
}

// assertLogFileLacks holds that the log file on disk does not carry secret.
func assertLogFileLacks(t *testing.T, secret string) {
	t.Helper()
	p := logging.Log.Path()
	if p == "" {
		return
	}
	kept, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(kept), secret) {
		t.Error("the log file carries the token")
	}
}

// settingsServerOn starts a server on a configuration that is already loaded, and hands
// back the server, the path of its file and the session a request has to carry.
func settingsServerOn(t *testing.T, cfg *config.Config) (*Server, string, string) {
	t.Helper()

	state := New(cfg)
	server := NewServer(state, "", nil)
	t.Cleanup(server.Stop)
	if _, err := server.URL(); err != nil {
		t.Fatalf("URL: %v", err)
	}
	return server, cfg.Path(), server.session
}

// TestARequestUnderAnotherNameIsRefused: the page is answered at the address it was
// served on, so a page a browser was made to reach at a name of its own gets nothing —
// which is what stops DNS rebinding from depending on what the browser does with a
// cookie.
func TestARequestUnderAnotherNameIsRefused(t *testing.T) {
	server, url := startedServer(t, "127.0.0.1:0")
	addr := strings.TrimPrefix(pageAddress(url), "http://")

	for _, host := range []string{addr, "localhost", "LOCALHOST"} {
		if !server.hostAllowed(host) {
			t.Errorf("host %q was refused", host)
		}
	}
	for _, host := range []string{"evil.example:8080", "mypc.local:8080", ""} {
		if server.hostAllowed(host) {
			t.Errorf("host %q was allowed", host)
		}
	}

	// The check is in the handler chain, so a refusal is a reply and not a handler that
	// happens to ignore the name.
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Host = "evil.example:8080"
	rec := httptest.NewRecorder()
	server.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "invalid host") {
		t.Errorf("under another name: status = %d, body = %q, want a refusal about the host",
			rec.Code, rec.Body.String())
	}

	// The address the page is served on reaches the handler, which refuses the request
	// for the reason the page's own tests expect: no session.
	req = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Host = addr
	rec = httptest.NewRecorder()
	server.routes().ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "invalid host") {
		t.Errorf("the address the page is served on was refused as a host: %s", rec.Body.String())
	}
}

// TestAListenerOnEveryInterfaceAnswersAnAddressAndNotAName: with --settings=0.0.0.0:8080
// an operator reaches the page from another machine, so this machine's address has to
// be answered. A name is not answered, since a name is what a rebinding page owns.
func TestAListenerOnEveryInterfaceAnswersAnAddressAndNotAName(t *testing.T) {
	server := NewServer(New(&config.Config{}), "", nil)
	t.Cleanup(server.Stop)
	server.mu.Lock()
	server.bound = fakeAddr{"0.0.0.0:8080"}
	server.mu.Unlock()

	for _, host := range []string{"192.168.1.5:8080", "[fe80::1]:8080", "127.0.0.1:8080"} {
		if !server.hostAllowed(host) {
			t.Errorf("host %q was refused", host)
		}
	}
	for _, host := range []string{"evil.example:8080", "mypc.local:8080"} {
		if server.hostAllowed(host) {
			t.Errorf("host %q was allowed", host)
		}
	}
}

// TestThePageFilesAreNotCached: the script and the stylesheet belong to the executable,
// so a browser must not pair one of them with the API of another build.
func TestThePageFilesAreNotCached(t *testing.T) {
	_, url := startedServer(t, "127.0.0.1:0")

	client := browser(t)
	if resp, err := client.Get(url); err != nil {
		t.Fatalf("open the address: %v", err)
	} else {
		resp.Body.Close()
	}
	for _, name := range []string{"/static/app.js", "/static/app.css"} {
		resp, err := client.Get(pageAddress(url) + name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", name, resp.StatusCode)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", name, got)
		}
	}
}

// TestASaveCarryingSomethingElseThanJSONIsRefused covers the content types a page from
// another site may post without asking this server first: none of them is JSON, and a
// request that says it sent one of them is not this page.
func TestASaveCarryingSomethingElseThanJSONIsRefused(t *testing.T) {
	cfg, err := config.LoadConfig(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	server, path, session := settingsServerOn(t, cfg)
	body := `{"accounts":[{"username":"s1234567","password":"typed","isps":[""]}]}`

	for _, contentType := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", contentType)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
		req.Header.Set("X-Auth-Token", session)
		rec := httptest.NewRecorder()
		server.handleConfig(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s: status = %d, want 415: %s", contentType, rec.Code, rec.Body.String())
		}
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "typed") {
		t.Errorf("a refused request was written down:\n%s", stored)
	}

	// The type this page sends is taken, and so is a request that names no type at all.
	for _, contentType := range []string{"application/json; charset=utf-8", ""} {
		req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader([]byte(body)))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
		req.Header.Set("X-Auth-Token", session)
		rec := httptest.NewRecorder()
		server.handleConfig(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Content-Type %q: status = %d, want 200: %s", contentType, rec.Code, rec.Body.String())
		}
	}
}

// TestARequestCannotSayWhereAPasswordCameFrom: a source is this program's own word
// about an entry. A request that names one must not be believed, or a password typed
// into the page could be dropped from the file because the request called it one that
// came from somewhere else.
func TestARequestCannotSayWhereAPasswordCameFrom(t *testing.T) {
	cfg, err := config.LoadConfig(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	server, path, session := settingsServerOn(t, cfg)

	body := `{"accounts":[{"username":"s1234567","password":"typed","passwordSource":"file","isps":[""]}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader([]byte(body)))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	req.Header.Set("X-Auth-Token", session)
	rec := httptest.NewRecorder()
	server.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), "typed") {
		t.Errorf("the password the page typed was left out because the request named a source:\n%s", stored)
	}
}

// TestASaveFromThePageDoesNotWriteASecretGivenToTheRun is the promise a run started with
// --password makes, end to end: a save that comes out of the page leaves the value out of
// the file, and the account keeps working until the program stops.
func TestASaveFromThePageDoesNotWriteASecretGivenToTheRun(t *testing.T) {
	cfg, err := config.LoadConfig(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	// What the command line leaves behind: the password of this run, and its source.
	cfg.Accounts = []config.Account{{Username: "s1234567", Password: "run-only", PasswordSource: config.SourceRun, ISPs: []string{""}}}
	server, path, session := settingsServerOn(t, cfg)

	// What the page sends for an entry whose password field the reader left alone.
	body := `{"accounts":[{"username":"s1234567","password":"","isps":["@cucc"]}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader([]byte(body)))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	req.Header.Set("X-Auth-Token", session)
	rec := httptest.NewRecorder()
	server.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	live := server.state.Config().Accounts
	if len(live) != 1 || live[0].Password != "run-only" || live[0].PasswordSource != config.SourceRun {
		t.Errorf("Accounts = %+v, want the secret of this run kept in memory", live)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "run-only") {
		t.Errorf("a save from the page wrote the secret that was given to the run:\n%s", stored)
	}
	if !strings.Contains(rec.Body.String(), `"passwordSource":"run"`) {
		t.Errorf("the page is not told where the password is: %s", rec.Body.String())
	}
}
