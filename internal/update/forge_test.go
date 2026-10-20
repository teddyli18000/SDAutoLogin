package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// forgeStub answers the releases endpoint with the JSON it is given, so the
// client is tested against the shape the forge actually sends.
func forgeStub(t *testing.T, body string) (*Client, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/api/v1/repos/zqlan/SDAutoLogin/releases" {
			t.Errorf("asked for %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "zqlan/SDAutoLogin"), &hits
}

const threeReleases = `[
  {"tag_name":"v1.3.0-alpha.1","prerelease":true,"draft":false,
   "published_at":"2026-10-07T00:00:00Z",
   "assets":[{"name":"SHA256SUMS","size":10,"browser_download_url":"http://x/SHA256SUMS"}]},
  {"tag_name":"v1.2.0","prerelease":false,"draft":false,
   "published_at":"2026-09-24T00:00:00Z","assets":[]},
  {"tag_name":"v1.1.0","prerelease":false,"draft":false,
   "published_at":"2026-08-01T00:00:00Z","assets":[]}
]`

// The stable channel is for the program everyone has, so a pre-release is not
// offered on it even though it is the newest thing there is.
func TestTheStableChannelPassesOverPreReleases(t *testing.T) {
	c, _ := forgeStub(t, threeReleases)
	rel, ok, err := c.Newest(context.Background(), Stable, mustParse(t, "v1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rel.Tag != "v1.2.0" {
		t.Fatalf("got %q (ok=%v), want v1.2.0", rel.Tag, ok)
	}
}

// The channel that allows them takes the newest of any kind.
func TestThePrereleaseChannelTakesTheNewest(t *testing.T) {
	c, _ := forgeStub(t, threeReleases)
	rel, ok, err := c.Newest(context.Background(), Prerelease, mustParse(t, "v1.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rel.Tag != "v1.3.0-alpha.1" {
		t.Fatalf("got %q (ok=%v), want v1.3.0-alpha.1", rel.Tag, ok)
	}
}

// Nothing newer is the ordinary answer, and not a failure.
func TestNothingNewerIsNotAFailure(t *testing.T) {
	client, _ := forgeStub(t, threeReleases)
	for _, c := range []struct {
		ch   Channel
		have string
	}{
		{Stable, "v1.2.0"},
		{Stable, "v9.9.9"},
		{Prerelease, "v1.3.0-alpha.1"},
		{Prerelease, "v2.0.0"},
	} {
		rel, ok, err := client.Newest(context.Background(), c.ch, mustParse(t, c.have))
		if err != nil {
			t.Errorf("%s from %s: %v", c.ch, c.have, err)
			continue
		}
		if ok {
			t.Errorf("%s from %s offered %s", c.ch, c.have, rel.Tag)
		}
	}
}

// A tag the program cannot read is passed over rather than compared on a guess,
// which is how a machine would move backwards onto an older release.
func TestATagThatCannotBeReadIsPassedOver(t *testing.T) {
	c, _ := forgeStub(t, `[
	  {"tag_name":"nightly","prerelease":false,"draft":false,"assets":[]},
	  {"tag_name":"v1.2.0","prerelease":false,"draft":false,"assets":[]}
	]`)
	rel, ok, err := c.Newest(context.Background(), Stable, mustParse(t, "v1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rel.Tag != "v1.2.0" {
		t.Fatalf("got %q (ok=%v), want v1.2.0", rel.Tag, ok)
	}
}

// A draft is not published, so nobody is offered it.
func TestADraftIsNotOffered(t *testing.T) {
	c, _ := forgeStub(t, `[
	  {"tag_name":"v2.0.0","prerelease":false,"draft":true,"assets":[]},
	  {"tag_name":"v1.2.0","prerelease":false,"draft":false,"assets":[]}
	]`)
	rel, ok, err := c.Newest(context.Background(), Prerelease, mustParse(t, "v1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rel.Tag != "v1.2.0" {
		t.Fatalf("got %q (ok=%v), want v1.2.0", rel.Tag, ok)
	}
}

// The releases are ordered here rather than taken in the order they arrive: a
// patch to an older release, put out after a newer one, would otherwise be
// offered as an update to it.
func TestTheOrderTheForgeAnsweredInDoesNotDecide(t *testing.T) {
	c, _ := forgeStub(t, `[
	  {"tag_name":"v1.2.1","prerelease":false,"draft":false,"published_at":"2026-10-01T00:00:00Z","assets":[]},
	  {"tag_name":"v1.3.0","prerelease":false,"draft":false,"published_at":"2026-09-01T00:00:00Z","assets":[]}
	]`)
	rel, ok, err := c.Newest(context.Background(), Stable, mustParse(t, "v1.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rel.Tag != "v1.3.0" {
		t.Fatalf("got %q (ok=%v), want v1.3.0, the newer version", rel.Tag, ok)
	}
}

func TestAnAssetIsFoundByName(t *testing.T) {
	c, _ := forgeStub(t, threeReleases)
	rels, err := c.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, ok := rels[0].Find("SHA256SUMS")
	if !ok || a.Size != 10 || a.URL != "http://x/SHA256SUMS" {
		t.Fatalf("Find = %+v (ok=%v)", a, ok)
	}
	if _, ok := rels[0].Find("nothing"); ok {
		t.Error("Find answered true for an asset that is not there")
	}
}

// An instance that answers with something other than a release list is reported
// as an http status rather than carried into the message, since that body can be
// a page of HTML from a proxy in front of the forge.
func TestARefusedListIsReportedByStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "<html><body>502 Bad Gateway</body></html>")
	}))
	t.Cleanup(srv.Close)
	_, err := NewClient(srv.URL, "zqlan/SDAutoLogin").Releases(context.Background())
	if err == nil {
		t.Fatal("no error for a refused list")
	}
	if got := err.Error(); got != "the releases of zqlan/SDAutoLogin answered http 502" {
		t.Errorf("error = %q", got)
	}
}

func TestAChannelThatIsNotOneOfTheTwoIsRefused(t *testing.T) {
	for s, want := range map[string]bool{
		"stable": true, "prerelease": true,
		"": false, "Stable": false, "nightly": false, "beta": false,
	} {
		if got := ValidChannel(s); got != want {
			t.Errorf("ValidChannel(%q) = %v, want %v", s, got, want)
		}
	}
}
