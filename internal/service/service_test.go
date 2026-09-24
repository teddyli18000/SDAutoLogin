package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sdautologin/internal/config"
	"sdautologin/internal/logging"
)

// stubPortal is a portal the tests can steer. It answers the two endpoints the
// client uses and counts the requests, so a test can assert that no login was
// attempted as well as what the reply said.
type stubPortal struct {
	// online is what online_list reports before any login.
	online bool
	// afterLogin, when set, is the online_list result once a login has been
	// submitted. Pointing it at false reproduces the reply that accepts a login
	// and still calls the client offline.
	afterLogin *bool
	// omitResult leaves the result field out of the online_list reply, so the
	// session state is unknown.
	omitResult bool
	// loginBody is the raw reply of the login endpoint.
	loginBody string
	// loginBodies are the replies of the login endpoint, one per attempt, the
	// last one repeating. A refused first account followed by an accepted second
	// is the failover case.
	loginBodies []string
	// intercepted makes the reachability probe answer 200 instead of 204, the
	// way a captive portal does.
	intercepted bool

	mu        sync.Mutex
	queries   int
	logins    int
	attempted []string
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
			s.attempted = append(s.attempted, r.URL.Query().Get("user_account"))
			body := s.loginBody
			if len(s.loginBodies) > 0 {
				i := s.logins - 1
				if i >= len(s.loginBodies) {
					i = len(s.loginBodies) - 1
				}
				body = s.loginBodies[i]
			}
			_, _ = io.WriteString(w, body)
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
		Accounts: []config.Account{
			{Username: "s1234567", Password: "p@ss", ISPs: []string{"@zgyd"}},
		},
		RememberPassword:     true,
		PortalBaseURL:        srv.URL + "/",
		EportalURL:           srv.URL + "/eportal/",
		AccountPrefix:        ",0,",
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

// submitted is the user_account of every login request that reached the portal,
// in order, showing which account was tried when.
func (s *stubPortal) submitted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.attempted...)
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
	// The portal names the account the session is on, so the interface can mark
	// which entry of the list that is, even one this program did not sign in.
	if snap.EntryIndex != 0 || snap.Entry != "s1234567 (China Mobile)" {
		t.Errorf("Entry = %q at %d, want the configured entry", snap.Entry, snap.EntryIndex)
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
	if !strings.Contains(snap.Message, "signed in as") {
		t.Errorf("Message = %q", snap.Message)
	}
	if snap.EntryIndex != 0 {
		t.Errorf("EntryIndex = %d, want the entry that signed in", snap.EntryIndex)
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
	// The reason has to reach the snapshot: the page shows it on the entry, and
	// the tray and the log have nothing else to go on.
	if !strings.Contains(snap.Message, "wrong account or password") {
		t.Errorf("Message = %q, want the reason the portal gave", snap.Message)
	}
	if len(snap.Skipped) != 1 || snap.Skipped[0].Index != 0 {
		t.Fatalf("Skipped = %+v, want the entry that was refused", snap.Skipped)
	}
	if snap.Skipped[0].Message != "wrong account or password" {
		t.Errorf("Skipped[0].Message = %q", snap.Skipped[0].Message)
	}
	// The code travels with it, since the settings page says the same thing in
	// the language of its own interface.
	if snap.Skipped[0].RetCode != "1" {
		t.Errorf("Skipped[0].RetCode = %q, want the answer of the portal", snap.Skipped[0].RetCode)
	}
	if snap.Skipped[0].RetryInS <= 0 {
		t.Errorf("RetryInS = %d, want the wait that is left of the cooldown", snap.Skipped[0].RetryInS)
	}
}

func TestAnExpiredCooldownIsNotReported(t *testing.T) {
	// A cooldown can run out while the session stays up, so signIn is never
	// reached to forget it. The entry must not be reported as still being left
	// alone, and in particular not with no wait left.
	sp := &stubPortal{online: true}
	svc := NewService(sp.start(t))

	svc.mu.Lock()
	svc.rejected["expired"] = rejection{
		message: "wrong account or password",
		retCode: "1",
		index:   0,
		until:   time.Now().Add(-time.Minute),
	}
	svc.mu.Unlock()

	svc.tick()

	snap := svc.Snapshot()
	if len(snap.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want the expired cooldown dropped", snap.Skipped)
	}
	if len(svc.rejected) != 0 {
		t.Errorf("rejected = %+v, want the expired refusal forgotten", svc.rejected)
	}
}

func TestAnExpiredCooldownIsLoggedOnTheOfflinePath(t *testing.T) {
	// The check loop prunes through signIn before skipped ever sees the entries,
	// so the expiry has to be logged there as well: otherwise the very path an
	// operator watches while the session is down says nothing about the cleanup.
	dir := t.TempDir()
	logging.Log.Init(dir, true)
	t.Cleanup(logging.Log.CloseFile)

	sp := &stubPortal{online: false, loginBody: `{"result":0,"ret_code":"1"}`}
	svc := NewService(sp.start(t))

	svc.mu.Lock()
	svc.rejected["expired"] = rejection{
		message: "wrong account or password",
		retCode: "1",
		index:   0,
		until:   time.Now().Add(-time.Minute),
	}
	svc.mu.Unlock()

	svc.tick()

	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if !strings.Contains(string(data), "out of its") {
		t.Errorf("the log does not report the cooldown being over:\n%s", data)
	}
	// The entry was free again, so it was submitted in this same round.
	if _, logins := sp.counts(); logins != 1 {
		t.Errorf("logins = %d, want the expired entry tried again", logins)
	}
}

func TestTickMovesOnToTheNextAccount(t *testing.T) {
	// The first account is refused for a reason of its own, so the second one is
	// tried in the same round.
	sp := &stubPortal{
		online:      false,
		afterLogin:  boolPtr(true),
		loginBodies: []string{`{"result":0,"ret_code":"1"}`, `{"result":1,"ret_code":"0"}`},
	}
	cfg := twoAccounts(sp.start(t))
	svc := NewService(cfg)

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateOnline {
		t.Fatalf("State = %v, want Online (message %q)", snap.State, snap.Message)
	}
	if snap.EntryIndex != 1 {
		t.Errorf("EntryIndex = %d, want the second entry", snap.EntryIndex)
	}
	if got := sp.submitted(); len(got) != 2 || got[0] != ",0,s1234567@zgyd" || got[1] != ",0,s7654321" {
		t.Errorf("the portal was asked for %v, want the first account and then the second", got)
	}
	if len(snap.Skipped) != 1 || snap.Skipped[0].Index != 0 {
		t.Errorf("Skipped = %+v, want the first entry left alone", snap.Skipped)
	}
}

func TestTickWalksPastAnAccountThatWasRefused(t *testing.T) {
	// Once an entry is known to be broken, the next round must not submit it
	// again, so a wrong password is not tried every check interval; that is the
	// cooldown's job.
	sp := &stubPortal{
		online:      false,
		afterLogin:  boolPtr(true),
		loginBodies: []string{`{"result":0,"ret_code":"1"}`, `{"result":1,"ret_code":"0"}`},
	}
	svc := NewService(twoAccounts(sp.start(t)))

	svc.tick() // refuses the first entry, signs in with the second
	svc.tick() // the session is up, so nothing is submitted either

	if got := sp.submitted(); len(got) != 2 {
		t.Errorf("the portal was asked for %v, want the refused entry to be left alone", got)
	}
}

func TestATemporaryFailureDoesNotWalkTheList(t *testing.T) {
	// A portal that is busy is not an account that is broken. Walking the list
	// on that answer would mark every account as refused in a single round.
	sp := &stubPortal{online: false, loginBodies: []string{`{"result":0,"ret_code":"3"}`}}
	svc := NewService(twoAccounts(sp.start(t)))

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateError {
		t.Fatalf("State = %v, want Error", snap.State)
	}
	if len(snap.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want the entries left in the running", snap.Skipped)
	}
	if got := sp.submitted(); len(got) != 1 {
		t.Errorf("the portal was asked for %v, want only the first account", got)
	}
	// The next round tries the same entry again, since nothing about it is known
	// to be wrong.
	svc.tick()
	if got := sp.submitted(); len(got) != 2 {
		t.Errorf("the portal was asked for %v, want the entry tried again", got)
	}
}

func TestAskingForACheckGivesARefusedEntryAnotherChance(t *testing.T) {
	// A check asked for by hand usually follows the fix, so the wait is cut
	// short.
	sp := &stubPortal{online: false, loginBody: `{"result":0,"ret_code":"1"}`}
	svc := NewService(sp.start(t))

	svc.tick()
	if len(svc.rejected) != 1 {
		t.Fatalf("rejected = %+v, want the entry the portal refused", svc.rejected)
	}

	svc.Trigger()

	if len(svc.rejected) != 0 {
		t.Errorf("rejected = %+v, want the refusals forgotten", svc.rejected)
	}
}

func TestANewConfigurationForgetsWhatWasRefused(t *testing.T) {
	// Saving the settings is the other way a fix reaches the program, and the
	// configuration it installs here names no account at all, so the check the
	// new loop runs straight away cannot put a refusal back.
	sp := &stubPortal{online: false, loginBody: `{"result":0,"ret_code":"1"}`}
	cfg := sp.start(t)
	svc := NewService(cfg)

	svc.tick()
	if len(svc.rejected) != 1 {
		t.Fatalf("rejected = %+v, want the entry the portal refused", svc.rejected)
	}

	empty := *cfg
	empty.Accounts = nil
	svc.Restart(&empty)
	svc.Stop()

	if len(svc.rejected) != 0 {
		t.Errorf("rejected = %+v, want the refusals forgotten", svc.rejected)
	}
}

// twoAccounts adds a second account to a test configuration for the failover
// tests: one entry that can be refused and another that follows it.
func twoAccounts(cfg *config.Config) *config.Config {
	cfg.Accounts = append(cfg.Accounts,
		config.Account{Username: "s7654321", Password: "other", ISPs: []string{""}})
	return cfg
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
	cfg.Accounts = nil
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
		Accounts: []config.Account{
			{Username: "s1234567", Password: "p@ss", ISPs: []string{""}},
		},
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

// TestStopWaitsForTheCheckThatIsRunning covers what a restart does: saving the
// settings stops the loop and starts another one, and the loop that was stopped
// has to be gone before the new configuration and client are handed over. A loop
// that is in the middle of a check would otherwise finish that check with the
// old passwords and the new endpoints, and both loops would be talking to the
// portal at once.
func TestStopWaitsForTheCheckThatIsRunning(t *testing.T) {
	entered := make(chan struct{})
	finished := make(chan struct{})
	var once, done sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "eportal") {
			_, _ = io.WriteString(w, "var v4ip = '203.0.113.7';")
			return
		}
		once.Do(func() { close(entered) })
		// Long enough that a Stop which does not wait returns while this is
		// still in flight, which is the difference the test is about.
		time.Sleep(200 * time.Millisecond)
		done.Do(func() { close(finished) })
		_, _ = io.WriteString(w, `{"result":"1","user_account":"s1234567","online_ip":"203.0.113.7"}`)
	}))
	t.Cleanup(srv.Close)

	svc := NewService(&config.Config{
		Accounts:             []config.Account{{Username: "s1234567", Password: "p@ss", ISPs: []string{""}}},
		PortalBaseURL:        srv.URL + "/",
		EportalURL:           srv.URL + "/eportal/",
		AccountPrefix:        ",0,",
		CheckIntervalSeconds: 5,
		HTTPTimeoutSeconds:   5,
		AutoLogin:            true,
		JsVersion:            "3.3.3",
	})
	svc.Start()

	<-entered
	svc.Stop()

	select {
	case <-finished:
	default:
		t.Error("Stop returned while a check of the loop it stopped was still running")
	}
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
