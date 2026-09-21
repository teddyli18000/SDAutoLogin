package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sdautologin/internal/config"
)

// stubPortal is a portal the tests can steer. It answers the two endpoints the
// client uses and counts the requests, so a test can assert that no login was
// attempted as well as what the reply said.
type stubPortal struct {
	// online is what online_list reports before any login.
	online bool
	// afterLogin, when set, is what online_list reports once a login has been
	// submitted. Pointing it at false reproduces the reply that accepts a login
	// and still calls the client offline.
	afterLogin *bool
	// omitResult leaves the result field out of the online_list reply, which is
	// what makes the session state unknown.
	omitResult bool
	// loginBody is the raw reply of the login endpoint.
	loginBody string
	// intercepted makes the reachability probe answer 200 instead of 204, the
	// way a captive portal does.
	intercepted bool

	mu      sync.Mutex
	queries int
	logins  int
}

func boolPtr(v bool) *bool { return &v }

func (s *stubPortal) start(t *testing.T) *config.Config {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "var v4ip = '203.0.113.7';")
	})
	mux.HandleFunc("/generate_204", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		intercepted := s.intercepted
		s.mu.Unlock()
		if intercepted {
			_, _ = io.WriteString(w, "<html>Dr.COM eportal</html>")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		if r.URL.Query().Get("a") == "login" {
			s.logins++
			_, _ = io.WriteString(w, s.loginBody)
			return
		}
		s.queries++
		if s.afterLogin != nil && s.logins > 0 {
			s.online = *s.afterLogin
		}
		if s.omitResult {
			_, _ = io.WriteString(w, `{"msg":"no result field in this reply"}`)
			return
		}
		result := "0"
		if s.online {
			result = "1"
		}
		fmt.Fprintf(w, `{"result":%s,"user_account":"s1234567",`+
			`"online_ip":"203.0.113.7","online_time":"2026-01-01 00:00:00"}`, result)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &config.Config{
		Username:             "s1234567",
		Password:             "p@ss",
		RememberPassword:     true,
		PortalBaseURL:        srv.URL + "/",
		EportalURL:           srv.URL + "/eportal/",
		AccountPrefix:        ",0,",
		AccountSuffix:        "@zgyd",
		CheckIntervalSeconds: 5,
		HTTPTimeoutSeconds:   5,
		AutoLogin:            true,
		JsVersion:            "3.3.3",
		ProbeURLs:            []string{srv.URL + "/generate_204"},
	}
}

func (s *stubPortal) counts() (queries, logins int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queries, s.logins
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

func TestTickReportsAnOnlineSession(t *testing.T) {
	sp := &stubPortal{online: true}
	svc := NewService(sp.start(t))

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateOnline {
		t.Fatalf("State = %v, want Online (message %q)", snap.State, snap.Message)
	}
	if snap.ClientIP != "203.0.113.7" || snap.Account != "s1234567" {
		t.Errorf("snapshot = %+v", snap)
	}
	if _, logins := sp.counts(); logins != 0 {
		t.Errorf("logins = %d, a login must not be attempted while the session is up", logins)
	}
}

func TestTickSignsBackInAfterADisconnection(t *testing.T) {
	sp := &stubPortal{online: false, afterLogin: boolPtr(true), loginBody: `{"result":1,"ret_code":"0"}`}
	svc := NewService(sp.start(t))

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateOnline {
		t.Fatalf("State = %v, want Online (message %q)", snap.State, snap.Message)
	}
	if snap.LastLoginAt == "" {
		t.Error("the successful login was not recorded")
	}
	if !strings.Contains(snap.Message, "signed in at") {
		t.Errorf("Message = %q", snap.Message)
	}
	if _, logins := sp.counts(); logins != 1 {
		t.Errorf("logins = %d, want exactly one attempt", logins)
	}
}

func TestTickKeepsReportingOfflineWhenTheLoginDidNotHelp(t *testing.T) {
	// The login endpoint can accept a request while the portal still reports the
	// client as offline, and that must not be shown as success.
	sp := &stubPortal{online: false, afterLogin: boolPtr(false), loginBody: `{"result":1,"ret_code":"0"}`}
	svc := NewService(sp.start(t))

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateError {
		t.Fatalf("State = %v, want Error (message %q)", snap.State, snap.Message)
	}
	if snap.Message != "portal accepted the login but still reports offline" {
		t.Errorf("Message = %q", snap.Message)
	}
}

func TestTickReportsARejectedLogin(t *testing.T) {
	sp := &stubPortal{online: false, loginBody: `{"result":0,"ret_code":"1"}`}
	svc := NewService(sp.start(t))

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateError {
		t.Fatalf("State = %v, want Error", snap.State)
	}
	if snap.LastError != "wrong account or password" {
		t.Errorf("LastError = %q", snap.LastError)
	}
	if !strings.Contains(snap.Message, "login failed") {
		t.Errorf("Message = %q", snap.Message)
	}
}

func TestTickDoesNotReconnectWhileTheStateIsUnknown(t *testing.T) {
	// A dropped session and an unreadable reply look the same at a glance.
	// Treating the second one as the first would fire a login on every transient
	// failure, so the round has to be skipped.
	sp := &stubPortal{online: false, omitResult: true}
	svc := NewService(sp.start(t))

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateError {
		t.Fatalf("State = %v, want Error", snap.State)
	}
	if !strings.Contains(snap.Message, "session state unknown") {
		t.Errorf("Message = %q", snap.Message)
	}
	if _, logins := sp.counts(); logins != 0 {
		t.Errorf("logins = %d, a login must not be attempted while the state is unknown", logins)
	}
}

func TestTickStaysOfflineWhenAutoLoginIsOff(t *testing.T) {
	sp := &stubPortal{online: false}
	cfg := sp.start(t)
	cfg.AutoLogin = false
	svc := NewService(cfg)

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateOffline {
		t.Fatalf("State = %v, want Offline", snap.State)
	}
	if snap.Message != "auto login is disabled" {
		t.Errorf("Message = %q", snap.Message)
	}
	if _, logins := sp.counts(); logins != 0 {
		t.Errorf("logins = %d, want none", logins)
	}
}

func TestTickReportsAMissingAccount(t *testing.T) {
	sp := &stubPortal{online: false}
	cfg := sp.start(t)
	cfg.Username = ""
	cfg.Password = ""
	svc := NewService(cfg)

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateError {
		t.Fatalf("State = %v, want Error", snap.State)
	}
	if !strings.Contains(snap.Message, "no account configured") {
		t.Errorf("Message = %q", snap.Message)
	}
	if _, logins := sp.counts(); logins != 0 {
		t.Errorf("logins = %d, want none", logins)
	}
}

func TestTickReportsAnUnreachablePortal(t *testing.T) {
	base := closedBase(t)
	cfg := &config.Config{
		Username:           "s1234567",
		Password:           "p@ss",
		PortalBaseURL:      base + "/",
		EportalURL:         base + "/eportal/",
		HTTPTimeoutSeconds: 5,
		AutoLogin:          true,
	}
	svc := NewService(cfg)

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateError {
		t.Fatalf("State = %v, want Error", snap.State)
	}
	if !strings.Contains(snap.LastError, "portal unreachable") {
		t.Errorf("LastError = %q", snap.LastError)
	}
}

func TestTickProbesTheInternetWhenAsked(t *testing.T) {
	sp := &stubPortal{online: true}
	cfg := sp.start(t)
	cfg.CheckInternet = true
	svc := NewService(cfg)

	svc.tick()

	snap := svc.Snapshot()
	if !snap.InternetChecked || !snap.InternetOK {
		t.Errorf("InternetChecked = %v, InternetOK = %v, want both true",
			snap.InternetChecked, snap.InternetOK)
	}
}

func TestTickReportsAnInterceptedProbeSeparatelyFromThePortal(t *testing.T) {
	// The portal can call the session online while something still intercepts
	// the rest of the traffic, and the status line has to say so.
	sp := &stubPortal{online: true, intercepted: true}
	cfg := sp.start(t)
	cfg.CheckInternet = true
	svc := NewService(cfg)

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateOnline {
		t.Fatalf("State = %v, want Online", snap.State)
	}
	if snap.InternetOK {
		t.Error("InternetOK = true although the probe was intercepted")
	}
	if snap.Message != "portal online, Internet unreachable" {
		t.Errorf("Message = %q", snap.Message)
	}
}

func TestTickPublishesToTheListener(t *testing.T) {
	sp := &stubPortal{online: true}
	svc := NewService(sp.start(t))

	var got []Snapshot
	svc.SetListener(func(s Snapshot) { got = append(got, s) })

	svc.tick()

	if len(got) == 0 {
		t.Fatal("the listener was never called")
	}
	if last := got[len(got)-1]; last.State != StateOnline {
		t.Errorf("the last snapshot published was %+v", last)
	}
}

func TestTriggerNeverBlocks(t *testing.T) {
	sp := &stubPortal{online: true}
	svc := NewService(sp.start(t))

	done := make(chan struct{})
	go func() {
		// The queue holds one request and further ones are dropped, so none of
		// these may wait for a check to finish.
		svc.Trigger()
		svc.Trigger()
		svc.Trigger()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Trigger blocked")
	}
}

func TestStartAndStopCanBeCalledTwice(t *testing.T) {
	sp := &stubPortal{online: true}
	svc := NewService(sp.start(t))

	svc.Start()
	svc.Start()
	svc.Stop()
	// Stopping again must not close a channel twice.
	svc.Stop()
}

func TestStateCodesAreStableAndUnique(t *testing.T) {
	// The code travels as the "state" field of the settings API and names the
	// i18n key "state.<code>", so it is part of two interfaces.
	want := map[State]string{
		StateUnknown:   "checking",
		StateOnline:    "online",
		StateOffline:   "offline",
		StateLoggingIn: "loggingIn",
		StateError:     "error",
	}
	seen := map[string]State{}
	for st, code := range want {
		if got := st.Code(); got != code {
			t.Errorf("%v.Code() = %q, want %q", st, got, code)
		}
		if other, ok := seen[code]; ok {
			t.Errorf("%v and %v share the code %q", other, st, code)
		}
		seen[code] = st
	}
}
