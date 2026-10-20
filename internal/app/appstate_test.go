package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"sdautologin/internal/config"
)

// stubPortal is a portal the tests can steer: what it reports the session as,
// what it answers a login with, and the accounts that were submitted, so a test
// can tell "nothing was submitted" from "the reply was a refusal".
type stubPortal struct {
	cfg *config.Config

	mu        sync.Mutex
	logins    int
	submitted []string
}

func (s *stubPortal) loginCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

func (s *stubPortal) accounts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.submitted...)
}

// onlineSession and offlineSession are the two sessions the tests care about
// most: one signed in as the configured account with no suffix (the campus network
// itself), and one that is not signed in.
const (
	onlineSession  = "s1234567"
	offlineSession = ""
)

// newStubPortal serves the two endpoints the client uses. session is the account
// online_list echoes: no suffix means signed in as the campus network itself, and
// an empty session is a client the portal reports as not signed in.
func newStubPortal(t *testing.T, isps []string, session, loginReply string) *stubPortal {
	t.Helper()

	stub := &stubPortal{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "var v4ip = '203.0.113.7';")
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("a") == "login" {
			stub.mu.Lock()
			stub.logins++
			stub.submitted = append(stub.submitted, r.URL.Query().Get("user_account"))
			stub.mu.Unlock()
			_, _ = io.WriteString(w, loginReply)
			return
		}
		if session == "" {
			_, _ = io.WriteString(w, `{"result":"0","msg":"the online data is empty"}`)
			return
		}
		fmt.Fprintf(w, `{"result":1,"user_account":%q,"online_ip":"203.0.113.7",`+
			`"online_time":"2026-01-01 00:00:00"}`, session)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	stub.cfg = &config.Config{
		Accounts:             []config.Account{{Username: "s1234567", Password: "p@ss", ISPs: isps}},
		PortalBaseURL:        srv.URL + "/",
		EportalURL:           srv.URL + "/eportal/",
		AccountPrefix:        ",0,",
		CheckIntervalSeconds: 5,
		HTTPTimeoutSeconds:   5,
	}
	return stub
}

// TestTheLoginTestSubmitsNothingWhileTheClientIsOnline pins the rule the check
// loop follows: the portal allows one session per address, so a second login
// comes back as a refusal that would tell the user their password is wrong.
func TestTheLoginTestSubmitsNothingWhileTheClientIsOnline(t *testing.T) {
	stub := newStubPortal(t, []string{"@zgyd"}, onlineSession, `{"result":0,"ret_code":1}`)

	test := New(stub.cfg).TestLogin(stub.cfg.Attempts()[0])

	if !test.AlreadyOnline {
		t.Error("AlreadyOnline = false for a portal that reports this client signed in")
	}
	if got := stub.loginCount(); got != 0 {
		t.Errorf("a login was submitted %d time(s) while the client was already online", got)
	}
	if !test.Before.Online || !test.After.Online {
		t.Errorf("the status around the test was not kept: before=%v after=%v", test.Before.Online, test.After.Online)
	}
}

// TestTheLoginTestSubmitsWhileTheClientIsOffline is the other half of it: the
// refusal above must not have turned the test off altogether.
func TestTheLoginTestSubmitsWhileTheClientIsOffline(t *testing.T) {
	stub := newStubPortal(t, []string{"@zgyd"}, offlineSession, `{"result":0,"ret_code":1}`)

	test := New(stub.cfg).TestLogin(stub.cfg.Attempts()[0])

	if test.AlreadyOnline {
		t.Error("AlreadyOnline = true for a portal that reports this client offline")
	}
	if got := stub.loginCount(); got != 1 {
		t.Errorf("logins = %d, want the one attempt the test is for", got)
	}
	if test.Result.RetCode != "1" || test.Result.Success {
		t.Errorf("the portal's refusal was not reported: %+v", test.Result)
	}
}

// TestSwitchServiceSignsInAgainAsTheFirstServiceType is the switch a session that
// was already up needs: the portal keeps one session per address, so the only way
// onto the first service type of an entry is a login that replaces the session.
func TestSwitchServiceSignsInAgainAsTheFirstServiceType(t *testing.T) {
	// The portal echoes what a login was submitted with, so a session that reads
	// back as the bare account is one that was signed in as the campus network
	// itself, while the first service type of this entry is China Mobile.
	stub := newStubPortal(t, []string{"@zgyd", ""}, onlineSession,
		`{"result":1,"ret_code":0,"msg":"authentication successful"}`)

	test, moved := New(stub.cfg).SwitchService("@zgyd")

	if !moved {
		t.Fatal("moved = false, want the session moved onto the first service type")
	}
	if got := stub.loginCount(); got != 1 {
		t.Errorf("logins = %d, want the one sign-in the switch is", got)
	}
	if got := stub.accounts(); len(got) != 1 || got[0] != ",0,s1234567@zgyd" {
		t.Errorf("submitted = %q, want the account with the service type that was asked for", got)
	}
	if test.Attempt.ISP != "@zgyd" {
		t.Errorf("the sign-in carried suffix %q, want the first one of the entry", test.Attempt.ISP)
	}
	if !test.Result.Success {
		t.Errorf("the portal's acceptance was not reported: %+v", test.Result)
	}
	if !test.Before.Online {
		t.Error("the session that was already up was not read before the sign-in")
	}
}

// TestSwitchServiceCanPickAnotherServiceTypeOfTheEntry covers the reader who
// picks one of the other service types, which the page offers when the first one
// is the one the portal will not take. A type the entry does not list is refused.
func TestSwitchServiceCanPickAnotherServiceTypeOfTheEntry(t *testing.T) {
	cases := []struct {
		name      string
		isps      []string
		session   string
		isp       string
		moved     bool
		submitted string
	}{
		{"another service type of the same entry", []string{"@zgyd", "@cucc"}, onlineSession,
			"@cucc", true, ",0,s1234567@cucc"},
		{"the campus network itself", []string{"@zgyd", ""}, "s1234567@zgyd",
			"", true, ",0,s1234567"},
		{"a service type the entry does not list", []string{"@zgyd", "@cucc"}, onlineSession,
			"@ctc", false, ""},
		{"the service type the session reads back as", []string{"@zgyd", "@cucc"}, onlineSession,
			"@zgyd", false, ""},
		{"a session on an account this configuration does not name", []string{"@zgyd", "@cucc"}, "23D001",
			"@cucc", false, ""},
		{"a session that is down", []string{"@zgyd", "@cucc"}, offlineSession,
			"@cucc", false, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := newStubPortal(t, c.isps, c.session,
				`{"result":1,"ret_code":0,"msg":"authentication successful"}`)

			test, moved := New(stub.cfg).SwitchService(c.isp)

			if moved != c.moved {
				t.Fatalf("moved = %v, want %v", moved, c.moved)
			}
			submitted := stub.accounts()
			if c.submitted == "" {
				if len(submitted) != 0 {
					t.Errorf("submitted = %q, want nothing with nothing to move", submitted)
				}
				return
			}
			if len(submitted) != 1 || submitted[0] != c.submitted {
				t.Errorf("submitted = %q, want %q", submitted, c.submitted)
			}
			if test.Attempt.ISP != c.isp {
				t.Errorf("the sign-in carried suffix %q, want %q", test.Attempt.ISP, c.isp)
			}
		})
	}
}
