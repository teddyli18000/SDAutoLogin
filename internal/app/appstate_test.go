package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"sdautologin/internal/config"
)

// portalStub answers the two endpoints the client uses and counts the logins it
// was sent, so a test can tell "nothing was submitted" from "the reply was a
// refusal".
func portalStub(t *testing.T, online bool) (*config.Config, func() int) {
	t.Helper()

	if online {
		return portalStubSession(t, []string{"@zgyd"}, "s1234567", `{"result":0,"ret_code":1}`)
	}
	return portalStubSession(t, []string{"@zgyd"}, "", `{"result":0,"ret_code":1}`)
}

// portalStubSession is portalStub with the session the portal reports and the
// reply a login gets under the test's control.
//
// session is the account online_list echoes, which is the account a login was
// submitted with: the portal echoes what it was given, so a session that reads
// back with no suffix on it is one that was signed in as the campus network
// itself. An empty session is a client the portal reports as not signed in.
func portalStubSession(t *testing.T, isps []string, session, loginReply string) (*config.Config, func() int) {
	t.Helper()

	logins := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "var v4ip = '203.0.113.7';")
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("a") == "login" {
			logins++
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

	cfg := &config.Config{
		Accounts:             []config.Account{{Username: "s1234567", Password: "p@ss", ISPs: isps}},
		PortalBaseURL:        srv.URL + "/",
		EportalURL:           srv.URL + "/eportal/",
		AccountPrefix:        ",0,",
		CheckIntervalSeconds: 5,
		HTTPTimeoutSeconds:   5,
	}
	return cfg, func() int { return logins }
}

// TestTheLoginTestSubmitsNothingWhileTheClientIsOnline pins the rule the check
// loop follows. The portal allows one session per address, so a second login on
// top of one comes back as a refusal that blames the account, and reporting that
// would tell the user their password is wrong while their session is up.
func TestTheLoginTestSubmitsNothingWhileTheClientIsOnline(t *testing.T) {
	cfg, logins := portalStub(t, true)

	test := New(cfg).TestLogin(cfg.Attempts()[0])

	if !test.AlreadyOnline {
		t.Error("AlreadyOnline = false for a portal that reports this client signed in")
	}
	if got := logins(); got != 0 {
		t.Errorf("a login was submitted %d time(s) while the client was already online", got)
	}
	if !test.Before.Online || !test.After.Online {
		t.Errorf("the status around the test was not kept: before=%v after=%v", test.Before.Online, test.After.Online)
	}
}

// TestTheLoginTestSubmitsWhileTheClientIsOffline is the other half of it: the
// refusal above must not have turned the test off altogether.
func TestTheLoginTestSubmitsWhileTheClientIsOffline(t *testing.T) {
	cfg, logins := portalStub(t, false)

	test := New(cfg).TestLogin(cfg.Attempts()[0])

	if test.AlreadyOnline {
		t.Error("AlreadyOnline = true for a portal that reports this client offline")
	}
	if got := logins(); got != 1 {
		t.Errorf("logins = %d, want the one attempt the test is for", got)
	}
	if test.Result.RetCode != "1" || test.Result.Success {
		t.Errorf("the portal's refusal was not reported: %+v", test.Result)
	}
}

// TestUsePreferredSignsInAgainAsTheFirstServiceType is the switch a session that
// was already up when the program started needs: the portal keeps one session
// per address, so the only way onto the first service type of an entry is a
// login that replaces the session, which is what the page's button asks for.
func TestUsePreferredSignsInAgainAsTheFirstServiceType(t *testing.T) {
	// The portal echoes what a login was submitted with, so a session that reads
	// back as the bare account is one that was signed in as the campus network
	// itself, while the first service type of this entry is China Mobile.
	cfg, logins := portalStubSession(t, []string{"@zgyd", ""}, "s1234567",
		`{"result":1,"ret_code":0,"msg":"authentication successful"}`)

	test, moved := New(cfg).UsePreferred()

	if !moved {
		t.Fatal("moved = false, want the session moved onto the first service type")
	}
	if got := logins(); got != 1 {
		t.Errorf("logins = %d, want the one sign-in the switch is", got)
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

// TestUsePreferredHasNothingToDoWhenThereIsNothingToMove covers the two ways the
// answer is no: the session is already on the first service type, or it is not
// up at all. Neither is a failure, and neither may submit a login, since a login
// on top of a session the portal is happy with comes back as a refusal that
// blames the account.
func TestUsePreferredHasNothingToDoWhenThereIsNothingToMove(t *testing.T) {
	cases := []struct {
		name    string
		isps    []string
		session string
	}{
		{"already on the first service type", []string{"@zgyd", ""}, "s1234567@zgyd"},
		{"the session is down", []string{"@zgyd", ""}, ""},
		{"the entry names one service type only", []string{"@zgyd"}, "s1234567"},
		{"the session is on an account this configuration does not name", []string{"@zgyd", ""}, "23D001"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, logins := portalStubSession(t, c.isps, c.session,
				`{"result":1,"ret_code":0,"msg":"authentication successful"}`)

			_, moved := New(cfg).UsePreferred()

			if moved {
				t.Error("moved = true, want nothing to move the session onto")
			}
			if got := logins(); got != 0 {
				t.Errorf("a login was submitted %d time(s) with nothing to move", got)
			}
		})
	}
}
