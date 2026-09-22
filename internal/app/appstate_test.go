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

	logins := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "var v4ip = '203.0.113.7';")
	})
	mux.HandleFunc("/eportal/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("a") == "login" {
			logins++
			_, _ = io.WriteString(w, `{"result":0,"ret_code":1}`)
			return
		}
		result := "0"
		if online {
			result = "1"
		}
		fmt.Fprintf(w, `{"result":%s,"user_account":"s1234567","online_ip":"203.0.113.7",`+
			`"online_time":"2026-01-01 00:00:00"}`, result)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		Accounts:             []config.Account{{Username: "s1234567", Password: "p@ss", ISPs: []string{"@zgyd"}}},
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
