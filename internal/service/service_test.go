package service

import (
	"context"
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
	"sdautologin/internal/update"
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
	// lag is how many online_list replies after a login still answer what the
	// portal said before it: the online list is written a moment behind the login
	// endpoint.
	lag int
	// omitResultAfterLogin leaves the result field out of the online_list reply
	// once a login has been submitted, so the session cannot be read back after
	// a login the portal accepted.
	omitResultAfterLogin bool
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
	// accepted is whether the login the stub answered last was one it took, by
	// the same rule the portal uses: the result field says 1 or ok.
	accepted bool
}

// loginAccepted reports whether a login reply is an acceptance. The stubs answer
// with the result field the portal itself is read by, so the rule is the one the
// portal package applies to it.
func loginAccepted(body string) bool {
	return strings.Contains(body, `"result":1`) || strings.Contains(body, `"result":"1"`) ||
		strings.Contains(body, `"result":"ok"`)
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
			s.accepted = loginAccepted(body)
			_, _ = io.WriteString(w, body)
			return
		}
		s.queries++
		if s.afterLogin != nil && s.logins > 0 {
			if s.lag > 0 {
				s.lag--
			} else {
				s.online = *s.afterLogin
			}
		}
		if s.omitResult || (s.omitResultAfterLogin && s.logins > 0) {
			_, _ = io.WriteString(w, `{"msg":"no result field in this reply"}`)
			return
		}
		result := "0"
		if s.online {
			result = "1"
		}
		// The portal echoes the account a login was submitted with, and the stub
		// does too, since which service type the session is on is read off that
		// echo. Only an accepted login changes it.
		account := "s1234567"
		if s.accepted && len(s.attempted) > 0 {
			account = strings.TrimPrefix(s.attempted[len(s.attempted)-1], ",0,")
		}
		fmt.Fprintf(w, `{"result":%s,"user_account":%q,`+
			`"online_ip":"203.0.113.7","online_time":"2026-01-01 00:00:00"}`, result, account)
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

func TestAnUnconfirmedLoginIsNotReportedAsSuccess(t *testing.T) {
	// The login endpoint can accept a request while the portal still reports the
	// client as offline. That must not become success, and it must not become a
	// failure either: a list that has not caught up reads the same as a login that
	// did not take.
	fastConfirm(t)
	sp := &stubPortal{online: false, afterLogin: boolPtr(false), loginBody: `{"result":1,"ret_code":"0"}`}
	svc := NewService(sp.start(t))

	svc.tick()

	snap := svc.Snapshot()
	if snap.State == StateOnline {
		t.Fatal("State = Online, and the portal never listed the session")
	}
	if snap.State != StateLoggingIn {
		t.Errorf("State = %v, want Signing in", snap.State)
	}
	if !strings.Contains(snap.Message, "not confirmed") {
		t.Errorf("Message = %q, want it to say the sign-in is unconfirmed", snap.Message)
	}
	// Nothing was refused, so nothing is counted against the account: the failure
	// streak is what the log escalates a real outage on.
	svc.mu.Lock()
	streak := svc.failStreak
	svc.mu.Unlock()
	if streak != 0 {
		t.Errorf("failStreak = %d, want 0", streak)
	}
}

// fastConfirm shortens the wait between the reads of the session that follow a
// login, so a test which has to go through it does not spend the second and a
// half the program waits.
func fastConfirm(t *testing.T) {
	t.Helper()
	previous := confirmDelay
	confirmDelay = time.Millisecond
	t.Cleanup(func() { confirmDelay = previous })
}

// TestTickSignsInWhenTheOnlineListLagsBehindTheLogin covers the portal that
// accepts a login and answers the session query before the session is in the
// list, so one read is not an answer and the login is not called a failure.
func TestTickSignsInWhenTheOnlineListLagsBehindTheLogin(t *testing.T) {
	fastConfirm(t)
	sp := &stubPortal{online: false, afterLogin: boolPtr(true), lag: 1,
		loginBody: `{"result":1,"ret_code":"0"}`}
	svc := NewService(sp.start(t))

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateOnline {
		t.Fatalf("State = %v, want Online (message %q)", snap.State, snap.Message)
	}
	if snap.LastLoginAt == "" {
		t.Error("the login that the lag hid was not recorded")
	}
	if svc.failStreak != 0 {
		t.Errorf("failStreak = %d, want a reply that lagged to leave no failure behind", svc.failStreak)
	}
	// The check's own query, the one that lagged, and the one that confirmed
	// the session.
	if queries, logins := sp.counts(); logins != 1 || queries < 3 {
		t.Errorf("queries=%d logins=%d, want the session asked for again after the login", queries, logins)
	}
}

// TestTickDoesNotCallALoginFailedWhenTheSessionCannotBeReadBack covers the other
// half of the same report: the portal accepted the login and cannot be asked
// about the session, so the state says signed in, not confirmed yet.
func TestTickDoesNotCallALoginFailedWhenTheSessionCannotBeReadBack(t *testing.T) {
	fastConfirm(t)
	sp := &stubPortal{online: false, omitResultAfterLogin: true,
		loginBody: `{"result":1,"ret_code":"0"}`}
	svc := NewService(sp.start(t))

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateLoggingIn {
		t.Fatalf("State = %v, want it to stay at signing in (message %q)", snap.State, snap.Message)
	}
	if !strings.Contains(snap.Message, "not confirmed yet") {
		t.Errorf("Message = %q, want it to say the login is not confirmed", snap.Message)
	}
	if svc.failStreak != 0 {
		t.Errorf("failStreak = %d, want a login that could not be read back to be no failure", svc.failStreak)
	}
}

// TestTickMovesASessionOntoTheServiceTypeTheEntryPutsFirst covers a session that
// was already up on a service type the entry does not put first. The portal keeps
// one session per client address, so following the list means signing in again.
func TestTickMovesASessionOntoTheServiceTypeTheEntryPutsFirst(t *testing.T) {
	sp := &stubPortal{online: true, loginBody: `{"result":1,"ret_code":"0"}`}
	cfg := sp.start(t)
	// The session is on the campus network, which the portal names without a
	// suffix, while the entry puts China Mobile first.
	cfg.Accounts[0].ISPs = []string{"@zgyd", ""}
	cfg.SwitchToPreferredService = true
	svc := NewService(cfg)

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateOnline {
		t.Fatalf("State = %v, want Online (message %q)", snap.State, snap.Message)
	}
	if !strings.Contains(snap.Message, "switched to") {
		t.Errorf("Message = %q, want it to say the session was moved", snap.Message)
	}
	if snap.EntryIndex != 0 || snap.Entry != "s1234567 (China Mobile)" {
		t.Errorf("Entry = %q at %d, want the entry the session was moved to", snap.Entry, snap.EntryIndex)
	}
	if snap.UsedISP != "@zgyd" || snap.MoveToPreferred {
		t.Errorf("the snapshot says the session is on %q and should be moved (%v), want it on the first service type and nothing left to do",
			snap.UsedISP, snap.MoveToPreferred)
	}
	if snap.SwitchRefused != nil {
		t.Errorf("the move that worked is still reported as refused: %+v", snap.SwitchRefused)
	}
	if got := sp.submitted(); len(got) != 1 || got[0] != ",0,s1234567@zgyd" {
		t.Errorf("submitted = %q, want one login as the first service type", got)
	}
}

// TestTickLeavesASessionAloneWhenTheOrderIsNotToBeEnforced is the same session
// with the option off: what is already up is left where it is, and the interface
// is left to say which service type that is.
func TestTickLeavesASessionAloneWhenTheOrderIsNotToBeEnforced(t *testing.T) {
	sp := &stubPortal{online: true, loginBody: `{"result":1,"ret_code":"0"}`}
	cfg := sp.start(t)
	cfg.Accounts[0].ISPs = []string{"@zgyd", ""}
	cfg.SwitchToPreferredService = false
	svc := NewService(cfg)

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateOnline {
		t.Fatalf("State = %v, want Online (message %q)", snap.State, snap.Message)
	}
	if strings.Contains(snap.Message, "switched") {
		t.Errorf("Message = %q, want the session to have been left alone", snap.Message)
	}
	if _, logins := sp.counts(); logins != 0 {
		t.Errorf("logins = %d, want none while the order is not enforced", logins)
	}
	// What the page renders the offer from: the service type the session is on,
	// the one it should be on, and whether there is anything to move onto, the
	// last being a flag of its own because the campus network has an empty suffix.
	if snap.UsedISP != "" || snap.PreferredISP != "@zgyd" || !snap.MoveToPreferred {
		t.Errorf("the snapshot offers used=%q preferred=%q move=%v, want the campus network moved onto China Mobile",
			snap.UsedISP, snap.PreferredISP, snap.MoveToPreferred)
	}
}

// TestTickLeavesASessionAloneWhenAutoLoginIsOff covers the other switch that
// answers whether this program signs in on its own: moving a session is a
// sign-in, so automatic login off leaves it to the page's button.
func TestTickLeavesASessionAloneWhenAutoLoginIsOff(t *testing.T) {
	sp := &stubPortal{online: true, loginBody: `{"result":1,"ret_code":"0"}`}
	cfg := sp.start(t)
	cfg.Accounts[0].ISPs = []string{"@zgyd", ""}
	cfg.SwitchToPreferredService = true
	cfg.AutoLogin = false
	svc := NewService(cfg)

	svc.tick()

	snap := svc.Snapshot()
	if snap.State != StateOnline {
		t.Fatalf("State = %v, want Online (message %q)", snap.State, snap.Message)
	}
	if _, logins := sp.counts(); logins != 0 {
		t.Errorf("logins = %d, want none while automatic login is off", logins)
	}
	if snap.PreferredISP != "@zgyd" || !snap.MoveToPreferred {
		t.Errorf("the offer is used=%q preferred=%q move=%v, want it left for the button",
			snap.UsedISP, snap.PreferredISP, snap.MoveToPreferred)
	}
}

// TestTickAsksAgainWhenThePortalWillNotSignInAgainWhileTheSessionIsUp covers the
// portal that refuses a second sign-in: the session is left as it is, the refusal
// is kept for the interface, and the entry stays usable. It is not asked every
// check.
func TestTickAsksAgainWhenThePortalWillNotSignInAgainWhileTheSessionIsUp(t *testing.T) {
	sp := &stubPortal{online: true,
		loginBody: `{"result":0,"ret_code":"1","msg":"wrong account or password"}`}
	cfg := sp.start(t)
	cfg.Accounts[0].ISPs = []string{"@zgyd", ""}
	cfg.SwitchToPreferredService = true
	svc := NewService(cfg)

	svc.tick()
	first := svc.Snapshot()
	if first.State != StateOnline {
		t.Fatalf("State = %v, want Online, the session is still a working one (message %q)", first.State, first.Message)
	}
	if !strings.Contains(first.Message, "would not sign in again") {
		t.Errorf("Message = %q, want it to say the portal refused the second sign-in", first.Message)
	}
	if first.SwitchRefused == nil {
		t.Fatal("the refusal was not kept, so nothing tells the reader the session needs them")
	}
	if first.SwitchRefused.RetCode != "1" || first.SwitchRefused.Message == "" {
		t.Errorf("SwitchRefused = %+v, want what the portal answered", first.SwitchRefused)
	}
	if len(svc.skipped(time.Now())) != 0 {
		t.Error("the entry was put out of the running for a refusal that was not about the account")
	}

	// The check right after it does not ask again.
	svc.tick()
	if _, logins := sp.counts(); logins != 1 {
		t.Errorf("logins = %d, want one: the portal must not be asked again within the wait", logins)
	}
	if svc.Snapshot().SwitchRefused == nil {
		t.Error("the refusal stopped being reported while the session is still where it was")
	}

	// Once the wait is over it is asked again.
	shortenSwitchWait(t)
	svc.tick()
	if _, logins := sp.counts(); logins != 2 {
		t.Errorf("logins = %d, want the move asked about again when the wait is over", logins)
	}
}

// shortenSwitchWait makes the wait between two moves of a session nothing, so a
// test does not have to spend it.
func shortenSwitchWait(t *testing.T) {
	t.Helper()

	previous := switchRetryPeriod
	switchRetryPeriod = 0
	t.Cleanup(func() { switchRetryPeriod = previous })
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
	// the tray and the log have only that to go on.
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
	recordWaits(svc)

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
	recordWaits(svc)

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
	// Saving the settings is the other way a fix reaches the program. The
	// configuration installed here names no account, so the check the new loop
	// runs straight away cannot put a refusal back.
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
	// Not a fault either. The tray colours this state as it colours Offline, so a
	// portal that answers 502 for a minute does not turn the icon red.
	if snap.State != StateNotKnown {
		t.Fatalf("State = %v, want the state not known", snap.State)
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
// settings stops the loop and starts another one, and the stopped loop has to be
// gone before the new configuration and client are handed over.
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
		// still in flight, the case the test is about.
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
		StateNotKnown:  "unknown",
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

// Two saves arriving together, which two browser tabs produce, used to leave
// the second overwriting the configuration of a loop the first had started.
// Needs -race to catch a return of it.
func TestRestartsArrivingTogetherLeaveOneLoop(t *testing.T) {
	sp := &stubPortal{online: true}
	base := sp.start(t)
	// A long interval keeps the loop parked on the timer and not in a check,
	// so the restarts are what the test is measuring.
	base.CheckIntervalSeconds = 3600

	newCfg := func(user string) *config.Config {
		cfg := base.Clone()
		cfg.Accounts[0].Username = user
		return cfg
	}

	svc := NewService(newCfg("first"))
	svc.Start()
	defer svc.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.Restart(newCfg("second"))
		}()
	}
	wg.Wait()

	// One loop, running the last configuration that was installed.
	svc.mu.Lock()
	running, cfg := svc.running, svc.cfg
	svc.mu.Unlock()
	if !running {
		t.Error("no loop is running after the restarts")
	}
	if cfg.Accounts[0].Username != "second" {
		t.Errorf("the loop is running %q, want the configuration that was saved last", cfg.Accounts[0].Username)
	}
}

// A code outside the table is a guess, and the log used to state it as a fact.
// An entry that keeps drawing one is never passed over, so this line is the
// only place that says what happened.
func TestAnUnknownRetCodeIsReportedAsUnknown(t *testing.T) {
	dir := t.TempDir()
	logging.Log.Init(dir, true)
	t.Cleanup(logging.Log.CloseFile)

	sp := &stubPortal{online: false, loginBody: `{"result":0,"ret_code":"99"}`}
	svc := NewService(sp.start(t))
	svc.tick()

	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "not one of the codes this program knows") {
		t.Errorf("the log does not say the code is unknown:\n%s", log)
	}
	if strings.Contains(log, "portal-side failure") {
		t.Errorf("the log still states a guess as a fact:\n%s", log)
	}
	// The list is not walked either way, which is the behaviour the reader has to
	// be able to predict from the line.
	if _, logins := sp.counts(); logins != 1 {
		t.Errorf("logins = %d, want the round to stop at the first entry", logins)
	}
}

// A code the portal does document is named, with what the portal calls it, so the
// two cases can be told apart in the log.
func TestAKnownRetCodeIsReportedWithItsMeaning(t *testing.T) {
	dir := t.TempDir()
	logging.Log.Init(dir, true)
	t.Cleanup(logging.Log.CloseFile)

	sp := &stubPortal{online: false, loginBody: `{"result":0,"ret_code":"3"}`}
	svc := NewService(sp.start(t))
	svc.tick()

	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if log := string(data); !strings.Contains(log, "portal busy, try again later") {
		t.Errorf("the log does not name the answer:\n%s", log)
	}
}

// A portal that cannot be read for a while is not a session that dropped. The
// outage was measured from the last online reading, so every round whose answer
// could not be read claimed the session had been down for that long, and the
// round that found it online again claimed it had come back. Nothing had
// happened to the session.
func TestAnUnreadableRoundDoesNotClaimTheSessionDropped(t *testing.T) {
	dir := t.TempDir()
	logging.Log.Init(dir, true)
	t.Cleanup(logging.Log.CloseFile)

	sp := &stubPortal{online: true}
	svc := NewService(sp.start(t))

	// One round that reads, so there is a last online reading to measure from.
	svc.tick()

	sp.omitResult = true
	svc.tick()
	svc.tick()

	sp.omitResult = false
	svc.tick()

	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	log := string(data)
	if strings.Contains(log, "offline for") {
		t.Errorf("the log says the session was offline, and it never was:\n%s", log)
	}
	if strings.Contains(log, "back online") {
		t.Errorf("the log claims a recovery that never happened:\n%s", log)
	}
	if !strings.Contains(log, "the state has not been readable for") {
		t.Errorf("the log does not say what could not be read:\n%s", log)
	}
	// The reading that could not be made is still not a fault, so the interface
	// is not shown one.
	if got := svc.Snapshot().State; got != StateOnline {
		t.Errorf("State = %v, want it back to Online", got)
	}
}

// stubForge is the release page the tests drive, standing in for the look
// internal/app hands the loop. It answers with what the test set and remembers
// what it was asked, so a test can assert that the forge was left alone as well
// as what it was told.
type stubForge struct {
	// client, current and target drive a real look instead: a client pointed at
	// a closed address is a release page that cannot be reached, the case the
	// loop has to survive.
	client  *update.Client
	current update.Version
	target  update.Target
	// check is what a look answers with, and checkErr how a look fails.
	check    update.Check
	checkErr error
	// installed is the program file an install reports, installErr how one fails.
	installed  string
	installErr error
	// block, when set, holds a look open until it is closed, so a test can see
	// what the loop does while a release page is answering slowly.
	block chan struct{}

	mu       sync.Mutex
	channels []update.Channel
	// ctxs is the context of every look, so a test can see that a look given up
	// on was cancelled as well as abandoned.
	ctxs     []context.Context
	releases []update.Release
}

func (f *stubForge) Check(ctx context.Context, ch update.Channel) (update.Check, error) {
	f.mu.Lock()
	f.channels = append(f.channels, ch)
	f.ctxs = append(f.ctxs, ctx)
	client, current, target := f.client, f.current, f.target
	check, err := f.check, f.checkErr
	block := f.block
	f.mu.Unlock()

	// Outside the lock: the test that set this is the one that closes it.
	if block != nil {
		<-block
	}

	if client != nil {
		return client.Check(ctx, ch, current, target)
	}
	return check, err
}

func (f *stubForge) Install(_ context.Context, rel update.Release) (string, error) {
	f.mu.Lock()
	f.releases = append(f.releases, rel)
	installed, err := f.installed, f.installErr
	f.mu.Unlock()
	return installed, err
}

// asked is the channel of every look, in the order they were made.
func (f *stubForge) asked() []update.Channel {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]update.Channel(nil), f.channels...)
}

// contexts is the context of every look, in the order they were made.
func (f *stubForge) contexts() []context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]context.Context(nil), f.ctxs...)
}

// installedReleases is every release the forge was asked to install.
func (f *stubForge) installedReleases() []update.Release {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]update.Release(nil), f.releases...)
}

// newLook is a release the test forge offers, with the flags a test sets.
func newLook(newer, signed, installable bool) update.Check {
	return update.Check{
		Current: "1.3.0-alpha.1",
		Latest:  "1.4.0",
		Channel: update.Stable,
		Newer:   newer,
		Signed:  signed,
		Release: update.Release{Tag: "v1.4.0"},
		// The reason names a package manager, which is the ordinary answer on a
		// platform whose builds arrive inside a package.
		Install: update.Install{
			Possible: installable,
			Reason:   "this copy was installed by the package manager",
		},
	}
}

// updateService is a service looking for a release: an online portal, the switch
// the reader turned on, and the release page handed in.
func updateService(t *testing.T, forge *stubForge) *Service {
	t.Helper()
	sp := &stubPortal{online: true, loginBody: `{"result":1,"ret_code":"0"}`}
	cfg := sp.start(t)
	cfg.CheckForUpdates = true
	svc := NewService(cfg)
	svc.SetUpdater(forge)
	return svc
}

// updateRound is the two checks a look at the release page takes: the one that
// starts it, and the next one, which publishes what it found. The look runs on a
// goroutine of its own, so a test waits for it between the two.
func updateRound(svc *Service) Snapshot {
	svc.tick()
	svc.updateWG.Wait()
	svc.tick()
	return svc.Snapshot()
}

// mustVersion is a version a test compares against.
func mustVersion(t *testing.T, tag string) update.Version {
	t.Helper()
	v, err := update.ParseVersion(tag)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", tag, err)
	}
	return v
}

func TestNoLookForAReleaseWhileTheSwitchIsOff(t *testing.T) {
	// The switch is the reader's, and it is off until they turn it on. With it
	// off the forge is not asked, so no check happens while they have not asked
	// for one.
	sp := &stubPortal{online: true, loginBody: `{"result":1,"ret_code":"0"}`}
	cfg := sp.start(t)
	forge := &stubForge{}
	svc := NewService(cfg)
	svc.SetUpdater(forge)

	snap := updateRound(svc)

	if got := forge.asked(); len(got) != 0 {
		t.Errorf("the release page was asked %v while checkForUpdates is off", got)
	}
	if snap.Update != nil {
		t.Errorf("Update = %+v, want nothing reported while the check is off", snap.Update)
	}
}

func TestTheReleasePageIsAskedOnTheConfiguredChannel(t *testing.T) {
	cases := []struct {
		setting string
		want    update.Channel
	}{
		// A configuration written by hand may hold anything, and a name this
		// program does not know must not decide which releases are offered.
		{"", update.Stable},
		{"stable", update.Stable},
		{"prerelease", update.Prerelease},
		{"beta", update.Stable},
		{"STABLE", update.Stable},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("updateChannel %q", c.setting), func(t *testing.T) {
			forge := &stubForge{}
			svc := updateService(t, forge)
			svc.cfg.UpdateChannel = c.setting

			updateRound(svc)

			got := forge.asked()
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("the release page was asked on %v, want one look on %s", got, c.want)
			}
		})
	}
}

func TestTheReleasePageIsAskedAtMostOnceBetweenLooks(t *testing.T) {
	forge := &stubForge{}
	svc := updateService(t, forge)

	svc.tick()
	svc.updateWG.Wait()
	svc.tick()
	svc.updateWG.Wait()

	if got := forge.asked(); len(got) != 1 {
		t.Fatalf("the release page was asked %d times over two checks, want one", len(got))
	}
	// The wait is what keeps a loop that runs every fifteen seconds from asking
	// a server the project pays for on every round.
	svc.mu.Lock()
	wait := time.Until(svc.nextUpdateAt)
	svc.mu.Unlock()
	if wait <= 0 || wait > updateInterval {
		t.Errorf("the next look is in %s, want it within %s", wait, updateInterval)
	}

	// Once the wait is over the forge is asked again.
	svc.mu.Lock()
	svc.nextUpdateAt = time.Now().Add(-time.Second)
	svc.mu.Unlock()
	svc.tick()
	svc.updateWG.Wait()

	if got := forge.asked(); len(got) != 2 {
		t.Errorf("the release page was asked %d times, want one look more", len(got))
	}
}

func TestAReleasePageThatCannotBeReachedLeavesTheSessionAlone(t *testing.T) {
	sp := &stubPortal{online: true, loginBody: `{"result":1,"ret_code":"0"}`}
	cfg := sp.start(t)
	cfg.CheckForUpdates = true
	svc := NewService(cfg)
	// A real client pointed at an address that is closed is the release page a
	// machine with no route to the forge sees.
	svc.SetUpdater(&stubForge{
		client:  update.NewClient(closedBase(t), "zqlan/SDAutoLogin"),
		current: mustVersion(t, "1.3.0-alpha.1"),
		target:  update.Native(filepath.Join(t.TempDir(), "SDAutoLogin")),
	})

	snap := updateRound(svc)
	if snap.State != StateOnline {
		t.Errorf("State = %v, want the session as the portal reported it", snap.State)
	}
	if snap.LastError != "" {
		t.Errorf("LastError = %q, want no fault of the session", snap.LastError)
	}
	if snap.Update != nil {
		t.Errorf("Update = %+v, want nothing reported", snap.Update)
	}
	if queries, logins := sp.counts(); queries == 0 || logins != 0 {
		t.Errorf("queries=%d logins=%d, want a check and no login", queries, logins)
	}
}

func TestANewerReleaseIsInstalledWhenThisCopyCanTakeIt(t *testing.T) {
	forge := &stubForge{
		check:     newLook(true, true, true),
		installed: "/opt/sdautologin/SDAutoLogin",
	}
	svc := updateService(t, forge)

	snap := updateRound(svc)

	got := forge.installedReleases()
	if len(got) != 1 || got[0].Tag != "v1.4.0" {
		t.Fatalf("installed %v, want the release the check found", got)
	}
	if snap.Update == nil {
		t.Fatal("the look was not reported in the snapshot")
	}
	if snap.Update.Installed != "/opt/sdautologin/SDAutoLogin" {
		t.Errorf("Installed = %q, want the file that was written", snap.Update.Installed)
	}
	if !strings.Contains(snap.Update.Message, "takes effect on the next start") {
		t.Errorf("Message = %q, want it to say what was installed", snap.Update.Message)
	}
	if snap.State != StateOnline {
		t.Errorf("State = %v, want the session untouched by the install", snap.State)
	}
}

func TestAReleaseIsNotInstalledWithoutASigningKey(t *testing.T) {
	// The build the sources carry has no key compiled in, so a look reports the
	// release and downloads nothing: what the server handed over would be what
	// runs.
	forge := &stubForge{check: newLook(true, false, true)}
	svc := updateService(t, forge)

	snap := updateRound(svc)

	if got := forge.installedReleases(); len(got) != 0 {
		t.Errorf("installed %v, want nothing installed without a key", got)
	}
	if snap.Update == nil || !snap.Update.Newer || snap.Update.Signed {
		t.Fatalf("Update = %+v, want the release reported without a key", snap.Update)
	}
	if !strings.Contains(snap.Update.Message, "no signing key") {
		t.Errorf("Message = %q, want it to say the build carries no key", snap.Update.Message)
	}
	if snap.State != StateOnline {
		t.Errorf("State = %v, want the session untouched", snap.State)
	}
}

func TestAReleaseIsNotInstalledWhenThisCopyCannotReplaceItself(t *testing.T) {
	forge := &stubForge{check: newLook(true, true, false)}
	svc := updateService(t, forge)

	snap := updateRound(svc)

	if got := forge.installedReleases(); len(got) != 0 {
		t.Errorf("installed %v, want this copy to install nothing", got)
	}
	if snap.Update == nil {
		t.Fatal("the look was not reported in the snapshot")
	}
	if snap.Update.Installable {
		t.Error("Installable is true, and this copy cannot put a release in place")
	}
	if snap.Update.Reason == "" || !strings.Contains(snap.Update.Message, snap.Update.Reason) {
		t.Errorf("Update = %+v, want the reason this copy cannot install it", snap.Update)
	}
}

func TestAFailedInstallIsReportedAndLeavesTheSessionAlone(t *testing.T) {
	forge := &stubForge{
		check:      newLook(true, true, true),
		installErr: fmt.Errorf("the archive does not match the published digest"),
	}
	svc := updateService(t, forge)

	snap := updateRound(svc)
	if snap.Update == nil || snap.Update.Installed != "" {
		t.Errorf("Update = %+v, want it reported and nothing installed", snap.Update)
	}
	if snap.State != StateOnline || snap.LastError != "" {
		t.Errorf("State = %v, LastError = %q, want the session alone",
			snap.State, snap.LastError)
	}
}

// The look for a release is a download of a few megabytes, and the loop's job
// is to ask the portal whether the session is still up. The look runs on its
// own, so a release page answering slowly cannot hold up a check.
func TestALookForAReleaseDoesNotHoldUpACheck(t *testing.T) {
	release := make(chan struct{})
	forge := &stubForge{block: release}
	svc := updateService(t, forge)
	t.Cleanup(func() {
		// Released first, so the look ends whether or not it was waited for.
		close(release)
		svc.Stop()
	})

	// The first check is due a look, since no look has happened yet. It has to
	// come back while that look is still open.
	done := make(chan struct{})
	go func() {
		svc.tick()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a check of the portal waited for the look at the release page")
	}

	// The look is genuinely open, so the check above came back for the reason
	// this test is about and not because nothing was started.
	deadline := time.Now().Add(5 * time.Second)
	for len(forge.asked()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the release page was never asked, so this proves nothing")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-release:
		t.Fatal("the look had already finished, so this proves nothing")
	default:
	}
}

// The check button on the settings page is the reader asking, so it runs whether
// or not the automatic check is switched on, and it does not wait out the
// interval the automatic one keeps to.
func TestAManualLookRunsWithTheSwitchOff(t *testing.T) {
	sp := &stubPortal{online: true, loginBody: `{"result":1,"ret_code":"0"}`}
	cfg := sp.start(t)
	cfg.CheckForUpdates = false
	forge := &stubForge{check: update.Check{Current: "1.2.0", Newer: false}}
	svc := NewService(cfg)
	svc.SetUpdater(forge)

	if !svc.CheckForUpdateNow() {
		t.Fatal("the manual look was refused although there is a release page")
	}
	// The look runs on its own, so it is waited for and not assumed: what is under
	// test here is that it was started at all.
	svc.updateWG.Wait()
	if got := forge.asked(); len(got) != 1 {
		t.Errorf("the release page was asked %v, want exactly once", got)
	}
}

// A run with no release page to ask says so, and does not pretend a look
// happened.
func TestAManualLookWithoutAReleasePageIsRefused(t *testing.T) {
	sp := &stubPortal{online: true, loginBody: `{"result":1,"ret_code":"0"}`}
	svc := NewService(sp.start(t))
	if svc.CheckForUpdateNow() {
		t.Error("a look was reported as started although there is nothing to look with")
	}
}

// waitLog records the pauses a round makes, so a test can assert the wait between
// two accounts without spending it.
type waitLog struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (w *waitLog) sleep(d time.Duration) {
	w.mu.Lock()
	w.waits = append(w.waits, d)
	w.mu.Unlock()
}

func (w *waitLog) all() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.waits...)
}

// recordWaits makes the pauses of a round visible to the test instead of spent.
func recordWaits(svc *Service) *waitLog {
	w := &waitLog{}
	svc.mu.Lock()
	svc.sleep = w.sleep
	svc.mu.Unlock()
	return w
}

// shortenStopWaits makes the bounds Stop gives up a check and a look for a release
// within nothing a test has to spend.
func shortenStopWaits(t *testing.T, loop, updates time.Duration) {
	t.Helper()
	previousLoop, previousUpdates := stopWaitTimeout, updateStopTimeout
	stopWaitTimeout, updateStopTimeout = loop, updates
	t.Cleanup(func() {
		stopWaitTimeout, updateStopTimeout = previousLoop, previousUpdates
	})
}

// TestTheRoundWaitsBetweenTwoAccounts covers the throttle on a list of accounts: a
// portal that refuses an entry used to be handed the next one at once, so the whole
// list of passwords went out in one burst. The first account still goes out at
// once, and every pair after it is separated.
func TestTheRoundWaitsBetweenTwoAccounts(t *testing.T) {
	refused := `{"result":0,"ret_code":"1"}`
	sp := &stubPortal{online: false, loginBodies: []string{refused, refused, refused}}
	cfg := twoAccounts(sp.start(t))
	cfg.Accounts = append(cfg.Accounts, config.Account{Username: "s1111111", Password: "third", ISPs: []string{""}})
	svc := NewService(cfg)
	waits := recordWaits(svc)

	svc.tick()

	if got := sp.submitted(); len(got) != 3 {
		t.Fatalf("the portal was asked for %v, want all three entries tried in one round", got)
	}
	// One wait between the first and the second account and one between the second
	// and the third, and none before the first.
	got := waits.all()
	if len(got) != 2 {
		t.Fatalf("the round waited %v, want one wait between each pair of accounts", got)
	}
	for i, wait := range got {
		if wait < loginSpacing || wait > loginSpacing+loginSpacingJitter {
			t.Errorf("wait %d is %s, want it within [%s, %s]", i+1, wait,
				loginSpacing, loginSpacing+loginSpacingJitter)
		}
	}
}

// TestTheRoundDoesNotWaitBeforeTheFirstAccount is the other half of the throttle: an
// installation with one account never waits, and the account that is refused alone
// is submitted at once.
func TestTheRoundDoesNotWaitBeforeTheFirstAccount(t *testing.T) {
	sp := &stubPortal{online: false, loginBody: `{"result":0,"ret_code":"1"}`}
	svc := NewService(sp.start(t))
	waits := recordWaits(svc)

	svc.tick()

	if got := waits.all(); len(got) != 0 {
		t.Errorf("the round waited %v with a single account", got)
	}
	if _, logins := sp.counts(); logins != 1 {
		t.Errorf("logins = %d, want the single account submitted once", logins)
	}
}

// TestLoginSpacingStaysWithinItsBounds covers the function behind the throttle: the
// wait is never shorter than the floor and never longer than the floor plus the
// jitter, whatever a draw says, and a draw outside [0,1) is clamped rather than
// trusted.
func TestLoginSpacingStaysWithinItsBounds(t *testing.T) {
	floor, ceiling := loginSpacing, loginSpacing+loginSpacingJitter
	if got := spacingBeforeNextLogin(0); got != floor {
		t.Errorf("with no jitter the wait is %s, want the floor %s", got, floor)
	}
	if got := spacingBeforeNextLogin(1); got != ceiling {
		t.Errorf("with the largest draw the wait is %s, want %s", got, ceiling)
	}
	if got := spacingBeforeNextLogin(-1); got != floor {
		t.Errorf("a negative draw gave %s, want the floor %s", got, floor)
	}
	if got := spacingBeforeNextLogin(2); got != ceiling {
		t.Errorf("a draw above one gave %s, want the largest wait %s", got, ceiling)
	}
	for i := 0; i <= 1000; i++ {
		rnd := float64(i) / 1000
		if got := spacingBeforeNextLogin(rnd); got < floor || got > ceiling {
			t.Fatalf("a draw of %v gave %s, want it within [%s, %s]", rnd, got, floor, ceiling)
		}
	}
}

// TestTheBackoffDoublesAndStopsGrowing covers the sequence the loop grows its wait
// with: the first failed round waits the ordinary interval, so a single failed round
// is retried exactly as it was before there was any backoff, and every round after
// it waits twice as long as the one before, up to the cap.
func TestTheBackoffDoublesAndStopsGrowing(t *testing.T) {
	const base = 15 * time.Second
	want := []time.Duration{
		15 * time.Second,
		30 * time.Second,
		60 * time.Second,
		120 * time.Second,
		240 * time.Second,
		maxBackoff,
		maxBackoff,
		maxBackoff,
	}
	for i, w := range want {
		if got := nextBackoff(base, i+1, 0); got != w {
			t.Errorf("after %d failed rounds in a row the wait is %s, want %s", i+1, got, w)
		}
	}
	// A round that did not fail waits the interval it was given, and a base of zero
	// has no wait to grow.
	if got := nextBackoff(base, 0, 0.5); got != base {
		t.Errorf("with no failures the wait is %s, want the interval %s", got, base)
	}
	if got := nextBackoff(base, -1, 0.5); got != base {
		t.Errorf("with a negative count the wait is %s, want the interval %s", got, base)
	}
	if got := nextBackoff(0, 5, 0.5); got != 0 {
		t.Errorf("with no interval the wait is %s, want 0", got)
	}
	// An interval already past the cap does not grow past it.
	if got := nextBackoff(time.Hour, 1, 0); got != maxBackoff {
		t.Errorf("an interval above the cap gave %s, want %s", got, maxBackoff)
	}
}

// TestTheBackoffJitterIsAddedWithinItsBound covers the random part: it is added to
// the wait and never subtracted from it, it is never more than the share allowed,
// and the cap bounds the wait with the jitter on it as well.
func TestTheBackoffJitterIsAddedWithinItsBound(t *testing.T) {
	const base = 15 * time.Second
	allowed := time.Duration(float64(maxBackoff) * backoffJitterFraction)
	plain := nextBackoff(base, 4, 0)
	most := nextBackoff(base, 4, 1)
	if want := plain + time.Duration(float64(plain)*backoffJitterFraction); most != want {
		t.Errorf("the largest draw gave %s, want %s", most, want)
	}
	if most <= plain {
		t.Fatalf("the jitter is %s on top of %s, want it added", most-plain, plain)
	}
	for i := 1; i <= 1000; i++ {
		rnd := float64(i) / 1001
		if got := nextBackoff(base, 4, rnd); got < plain || got > most {
			t.Fatalf("a draw of %v gave %s, want it within [%s, %s]", rnd, got, plain, most)
		}
	}
	// A draw outside [0,1) is clamped: nothing waits longer than the cap plus the
	// share of it the jitter may add.
	if got, want := nextBackoff(time.Hour, 9, 2), maxBackoff+allowed; got != want {
		t.Errorf("with the largest draw on a long outage the wait is %s, want %s", got, want)
	}
}

// TestAFailedRoundMakesTheNextCheckWaitLonger covers the wiring: the round decides
// the wait the loop keeps, and a round that reads the portal puts the ordinary
// interval back.
func TestAFailedRoundMakesTheNextCheckWaitLonger(t *testing.T) {
	// The portal is reachable, says the session is down, and answers every login
	// with the code it documents for "portal busy": the account is not at fault, so
	// the round ends there and the wait grows.
	sp := &stubPortal{online: false, loginBody: `{"result":0,"ret_code":"3"}`}
	cfg := twoAccounts(sp.start(t))
	svc := NewService(cfg)
	base := time.Duration(cfg.CheckIntervalSeconds) * time.Second

	svc.tick()
	first := svc.waitBeforeNextCheck(base)
	if first < base || first > base+base/4 {
		t.Fatalf("after one failed round the next check is in %s, want it within [%s, %s]",
			first, base, base+base/4)
	}

	svc.tick()
	second := svc.waitBeforeNextCheck(base)
	if second < 2*base || second > 2*base+base/2 {
		t.Fatalf("after two failed rounds the next check is in %s, want it within [%s, %s]",
			second, 2*base, 2*base+base/2)
	}

	// The portal is read again, so there is nothing left to back off from.
	sp.online = true
	svc.tick()
	if got := svc.waitBeforeNextCheck(base); got != base {
		t.Errorf("after a round that read the portal the next check is in %s, want the interval %s", got, base)
	}
}

// TestAnUnreadableRoundAlsoWaitsLonger: a portal that answers nothing usable is the
// portal's own trouble as well, and the loop waits longer for it too.
func TestAnUnreadableRoundAlsoWaitsLonger(t *testing.T) {
	sp := &stubPortal{online: false, omitResult: true}
	cfg := sp.start(t)
	svc := NewService(cfg)
	base := time.Duration(cfg.CheckIntervalSeconds) * time.Second

	svc.tick()
	svc.tick()

	if got := svc.waitBeforeNextCheck(base); got < 2*base {
		t.Errorf("after two rounds that could not be read the next check is in %s, want at least %s", got, 2*base)
	}
}

// TestALongFailureRunSaysTheChecksAreSlower covers what a reader sees: the wait
// growing is said out loud when it first doubles, so a portal that is down is not
// mistaken for a program that is stuck.
func TestALongFailureRunSaysTheChecksAreSlower(t *testing.T) {
	dir := t.TempDir()
	logging.Log.Init(dir, true)
	t.Cleanup(logging.Log.CloseFile)

	base := closedBase(t)
	svc := NewService(&config.Config{
		PortalBaseURL:        base + "/",
		EportalURL:           base + "/eportal/",
		CheckIntervalSeconds: 5,
		HTTPTimeoutSeconds:   5,
		AutoLogin:            true,
	})

	svc.tick()
	svc.tick()

	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if log := string(data); !strings.Contains(log, "the portal has been unreachable for 2 checks in a row") {
		t.Errorf("the log does not say the portal keeps failing and the checks are slower:\n%s", log)
	}
}

// TestStopGivesUpOnACheckThatWillNotFinish covers the bound on closing: a portal
// that never answers must not hold the program open. The check is not killed (it
// cannot be, its request having no context), it is given up on, and the loop ends
// by itself as soon as the request does.
func TestStopGivesUpOnACheckThatWillNotFinish(t *testing.T) {
	release := make(chan struct{})
	var letGo sync.Once
	drop := func() { letGo.Do(func() { close(release) }) }
	entered := make(chan struct{})
	var once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "eportal") {
			_, _ = io.WriteString(w, "var v4ip = '203.0.113.7';")
			return
		}
		once.Do(func() { close(entered) })
		<-release
		_, _ = io.WriteString(w, `{"result":"0"}`)
	}))
	t.Cleanup(srv.Close)
	// Registered after the server, so it runs before it: the request that was left
	// in flight has to finish before the server may be closed.
	t.Cleanup(drop)

	svc := NewService(&config.Config{
		PortalBaseURL:        srv.URL + "/",
		EportalURL:           srv.URL + "/eportal/",
		CheckIntervalSeconds: 5,
		HTTPTimeoutSeconds:   30,
	})
	shortenStopWaits(t, 100*time.Millisecond, 100*time.Millisecond)

	svc.Start()
	<-entered

	asked := time.Now()
	stopped := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return while a check was in flight")
	}
	if elapsed := time.Since(asked); elapsed > time.Second {
		t.Errorf("Stop took %s while a check would not finish, want it to give up within the bound", elapsed)
	}

	svc.mu.Lock()
	done := svc.done
	svc.mu.Unlock()

	// Nothing is left behind: the loop that was given up on ends on its own once the
	// request it is in finishes.
	drop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Error("the loop that was given up on never ended")
	}
}

// TestStopDoesNotWaitForALookAtTheReleasePage covers the other thing that used to
// hold closing open: a release being downloaded. The look is cancelled and waited
// for only briefly, so a release page that does not answer does not keep the program
// from closing.
func TestStopDoesNotWaitForALookAtTheReleasePage(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	forge := &stubForge{block: release}
	svc := updateService(t, forge)
	shortenStopWaits(t, 100*time.Millisecond, 20*time.Millisecond)

	svc.Start()
	deadline := time.Now().Add(10 * time.Second)
	for len(forge.asked()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the release page was never asked, so this proves nothing")
		}
		time.Sleep(5 * time.Millisecond)
	}

	asked := time.Now()
	stopped := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return with a look at the release page in flight")
	}
	if elapsed := time.Since(asked); elapsed > time.Second {
		t.Errorf("Stop took %s with a look in flight, want it to give up within the bound", elapsed)
	}

	// The look was cancelled, so a download in flight is aborted rather than left
	// running behind a program that is closing.
	ctxs := forge.contexts()
	if len(ctxs) == 0 {
		t.Fatal("no look was recorded")
	}
	if ctxs[0].Err() == nil {
		t.Error("the look was not cancelled when the loop was stopped")
	}
}

// TestAManualLookAfterAStopIsNotCancelled: the context a stopped run leaves behind
// is cancelled, so the reader asking for a look then has to be given one of its own
// instead of a request that comes back cancelled.
func TestAManualLookAfterAStopIsNotCancelled(t *testing.T) {
	forge := &stubForge{check: update.Check{Current: "1.2.0", Newer: false}}
	svc := updateService(t, forge)

	svc.Start()
	svc.Stop()

	if !svc.CheckForUpdateNow() {
		t.Fatal("the manual look was refused although there is a release page")
	}
	svc.updateWG.Wait()

	ctxs := forge.contexts()
	if len(ctxs) < 2 {
		t.Fatalf("the release page was asked %d times, want the look of the loop and then the manual one", len(ctxs))
	}
	if err := ctxs[len(ctxs)-1].Err(); err != nil {
		t.Errorf("the manual look after a stop got a cancelled context: %v", err)
	}
}
