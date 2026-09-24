package app

import (
	"bytes"
	"encoding/json"
	"errors"
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

// browser is a client that keeps cookies, which is what the browser does between
// the address it was opened with and the page it is left on.
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

// TestTheAddressIsExchangedForASessionCookie covers what happens to the token
// the tray hands out. The address bar, the history and the Referer of anything
// the page links to are all the same string, so the token is taken out of it as
// soon as the page is served, and what the page goes on using is another value.
func TestTheAddressIsExchangedForASessionCookie(t *testing.T) {
	server, url := startedServer(t, "127.0.0.1:0")

	client := browser(t)
	// The redirect is left alone, so the reply that carries the cookie can be
	// read rather than the page it leads to.
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
// is not a way in a second time. The server makes the next one as it spends this
// one, which is what the tray hands out afterwards.
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
// to work, so the second cannot be handed the address the first has just used.
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
	// in an address, so nothing else reads it from there. What a caller would
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
	// agreeing to a cross site request, so a write that carries the cookie alone
	// is refused, which is the second thing it has to get past.
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

// TestTheExitButtonReachesTheCallbackThatWasSetLast covers the run that took
// over the server the tray had built. The callback the tray gave it posts a
// message to a window that never got a message loop, so the fallback replaces it
// with one that ends this run, and the page's exit button has to reach the new
// one rather than the old.
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

func TestEveryAnswerCarriesTheSecurityHeaders(t *testing.T) {
	// The page is opened with the token in its address, so an answer must keep
	// that address from travelling out in a Referer, must not let another page
	// frame it, and must name what it may load rather than trust its own markup.
	// The refusal is the answer a caller without the token reads, so it carries
	// them too.
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
	// The comparison is a constant time one and an empty pair of them matches,
	// so a token that was never made has to be refused before the comparison
	// runs: a server that was never started must not accept a caller who sends
	// no token at all.
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

// TestARefusedSaveLeavesTheRunningConfigAlone covers a request that is not
// accepted: the page posts the whole configuration back, so the running one has
// to come out of it untouched. Otherwise a password that was typed wrong would
// take the session down at the same moment the page says the save was refused.
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
	// not wrong by itself any more: that is how the page says "keep the one that
	// is stored", and an entry with no name has nothing to keep a password for.
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

// The page writes the password field and never reads it, so a stored secret is
// not part of what it is given: it is told that there is one, and nothing more.
// A password that never leaves the process cannot be read out of the browser, out
// of the page's own script, or out of a request the page makes.
func TestThePageIsNotGivenTheStoredPassword(t *testing.T) {
	cfg := &config.Config{
		Accounts:         []config.Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}},
		RememberPassword: true,
	}

	_, body := pageOf(t, cfg)
	if strings.Contains(body, "topsecret") {
		t.Errorf("the stored password was handed to the page: %s", body)
	}
	if !strings.Contains(body, `"hasPassword":true`) {
		t.Errorf("the page is not told that a password is stored: %s", body)
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
	if !strings.Contains(rec.Body.String(), `"hasPassword":true`) {
		t.Errorf("the page is not told that a password is stored: %s", rec.Body.String())
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
	if strings.Contains(body, `"hasPassword":true`) {
		t.Errorf("the page was told a password is stored, and the file holds none: %s", body)
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
	// account and the service type, and no password at all.
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

// postAction runs one action the page can ask for and hands back the reply, so
// the contract between the page and the handler is what is being read rather
// than the handler on its own.
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
// when there was, what the sign-in went out as. A session that is already on the
// service type asked for is answered the same way as one that is not up at all -
// with nothing to do - rather than with a login reply nobody asked for.
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

// TestASwitchWithoutAServiceTypeIsRefused covers the difference the request
// keeps: the campus network itself is a service type with no suffix, so a
// request that names no service type at all is a malformed one rather than a
// request for the campus network.
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
// portal will not sign in again: the page asks for the portal's own page, and
// what reaches the desktop is the address the program is configured with. The
// address in the request is ignored on purpose - a page that could name one
// would be a page that can hand the shell anything it likes.
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
// button: when the desktop will not take the address, the request is answered as
// a failure with a message key, so the page says the button did nothing instead
// of reporting a browser that never came up.
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
	// Read back from the file rather than from what was written into it: the
	// next start is what the switch has to hold for.
	reloaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SwitchToPreferredService {
		t.Error("the file still has the switch on, so the next start would move the session again")
	}
}

// TestAFirstSaveFromThePage covers what the page sends when nothing is
// configured yet, which is what the wizard does. The list a save carries is the
// list of cards, so an account that has not been put in it is an account the
// request does not have, and a password that was not put in the card is one the
// request does not carry either. Both are refused by name, and the refusal is
// what the page shows next to the field it is about: the one a reader met was
// "password is required" on an account whose password had just been typed into a
// card that was rebuilt underneath it.
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
// log. It has to name every field that was refused, in one order, so two refusals
// can be read against each other, and it says which field it is without ever
// carrying what was in it.
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
