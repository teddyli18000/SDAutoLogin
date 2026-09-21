package app

import (
	"net/http"
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

	state := New(&config.Config{Username: "s1234567", Password: "p@ss"})
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
