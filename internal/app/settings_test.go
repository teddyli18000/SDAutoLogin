package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sdautologin/internal/config"
)

// fakeAddr stands in for a listener address in the tests that only exercise the
// address formatting.
type fakeAddr struct{ s string }

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return a.s }

// startedServer returns a server listening on addr and the address it reports.
func startedServer(t *testing.T, addr string) string {
	t.Helper()

	state := New(&config.Config{Accounts: []config.Account{{Username: "s1234567", Password: "p@ss"}}})
	server := NewServer(state, addr, nil)
	t.Cleanup(server.Stop)

	url, err := server.URL()
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	return url
}

func TestServerServesThePageOnTheAddressItWasGiven(t *testing.T) {
	url := startedServer(t, "127.0.0.1:0")

	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Errorf("url = %q, want a loopback address", url)
	}
	if !strings.Contains(url, "?token=") {
		t.Errorf("url = %q, want the token in it", url)
	}
}

func TestServerRefusesARequestWithoutTheToken(t *testing.T) {
	// The token is all that stands between the page and any other program that
	// can reach the address, so it has to be required.
	url := startedServer(t, "127.0.0.1:0")
	base := url[:strings.Index(url, "/?")]

	allowed, err := http.Get(url)
	if err != nil {
		t.Fatalf("get with the token: %v", err)
	}
	defer allowed.Body.Close()
	if allowed.StatusCode != http.StatusOK {
		t.Errorf("with the token: status %d, want 200", allowed.StatusCode)
	}

	refused, err := http.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("get without the token: %v", err)
	}
	defer refused.Body.Close()
	if refused.StatusCode != http.StatusForbidden {
		t.Errorf("without the token: status %d, want 403", refused.StatusCode)
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

	// The empty password is the only thing wrong with this request.
	body := `{"accounts":[{"username":"s1234567","password":"","isps":["@cucc"]}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader([]byte(body)))
	req.Header.Set("X-Auth-Token", server.token)
	rec := httptest.NewRecorder()
	server.handleConfig(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "err.passwordRequired") {
		t.Errorf("the reply does not name the password: %s", rec.Body.String())
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
