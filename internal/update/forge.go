package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Channel is which releases an update is allowed to move to.
type Channel string

const (
	// Stable takes the newest release that is not a pre-release. The program the
	// reader already has is what everyone gets.
	Stable Channel = "stable"
	// Prerelease takes the newest release of any kind, so a version can be tried
	// before it is the one everyone gets.
	Prerelease Channel = "prerelease"
)

// ValidChannel is whether the setting names a channel this program knows. An
// unknown one is refused rather than treated as one of the two, which would
// decide for the reader which releases they are offered.
func ValidChannel(s string) bool {
	return Channel(s) == Stable || Channel(s) == Prerelease
}

// Release is one release of the program, as the forge describes it.
type Release struct {
	Tag         string
	Prerelease  bool
	Draft       bool
	PublishedAt time.Time
	Assets      []Asset
}

// Asset is one file attached to a release.
type Asset struct {
	Name string
	Size int64
	URL  string
}

// Find returns the asset with this name.
func (r Release) Find(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Client asks a forge for the releases of one repository.
type Client struct {
	// Base is the instance, such as https://git.zqlhub.com, and Repo is
	// owner/name.
	Base string
	Repo string
	// HTTP is the client used for every request. It is given a timeout here
	// rather than left to the caller, because every request this package makes
	// is one a person is waiting on.
	HTTP *http.Client
}

// NewClient is a client for one repository.
func NewClient(base, repo string) *Client {
	return &Client{
		Base: base,
		Repo: repo,
		// No deadline on the client: the releases list is a few kilobytes and an
		// archive is megabytes, so each request carries its own. It is this
		// package's own client and not the shared default one, which any package
		// in the program may replace, and it refuses a redirect that leaves the
		// host it asked.
		HTTP: newHTTPClient(),
	}
}

type forgeRelease struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// releasesLimit is how many releases are asked for. The forge answers newest
// first, so one page holds the newest whatever the channel is, and the count
// only has to cover the pre-releases a stable reader is skipping over.
const releasesLimit = 30

// maxListBytes bounds the list reply. It is a description of the releases, not
// the releases themselves, and thirty of them are a few tens of kilobytes.
const maxListBytes = 4 << 20

// Releases is the releases of the repository, newest first.
func (c *Client) Releases(ctx context.Context) ([]Release, error) {
	// The instance is named in the build and its answer is not signed, so the
	// address is checked before it is asked: a forge reached over plain http is
	// one whose answer anyone on the way can write.
	if err := checkForgeAddress(c.Base, c.Base); err != nil {
		return nil, err
	}
	address := fmt.Sprintf("%s/api/v1/repos/%s/releases?limit=%d", c.Base, c.Repo, releasesLimit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body can be a page of HTML from a proxy, so only the status is
		// carried into the message.
		return nil, fmt.Errorf("the releases of %s answered http %d", c.Repo, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxListBytes {
		return nil, fmt.Errorf("the releases of %s are larger than this program will read", c.Repo)
	}
	var raw []forgeRelease
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("the releases of %s could not be read: %w", c.Repo, err)
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		if r.Draft {
			continue
		}
		rel := Release{
			Tag:         r.TagName,
			Prerelease:  r.Prerelease,
			Draft:       r.Draft,
			PublishedAt: r.PublishedAt,
		}
		for _, a := range r.Assets {
			rel.Assets = append(rel.Assets, Asset{Name: a.Name, Size: a.Size, URL: a.URL})
		}
		out = append(out, rel)
	}
	return out, nil
}

// Newest is the newest release this channel allows and that is newer than the
// version already installed. It answers false when there is nothing to move to,
// which is the ordinary answer and not a failure.
//
// The releases are ordered here rather than taken in the order they arrive: the
// forge sorts by the date it published, and a patch to an older release put out
// afterwards would then be offered as an update to a newer one.
func (c *Client) Newest(ctx context.Context, ch Channel, current Version) (Release, bool, error) {
	all, err := c.Releases(ctx)
	if err != nil {
		return Release{}, false, err
	}
	type candidate struct {
		rel Release
		ver Version
	}
	var found []candidate
	for _, r := range all {
		v, err := ParseVersion(r.Tag)
		if err != nil {
			// A tag this program cannot read is passed over: it cannot be
			// compared, and comparing it on a guess is how a machine ends up
			// moving backwards.
			continue
		}
		// The tag carries the label and the forge marks the release as a
		// pre-release; either one is enough to keep it off the stable channel.
		if ch == Stable && (v.IsPrerelease() || r.Prerelease) {
			continue
		}
		if !v.NewerThan(current) {
			continue
		}
		found = append(found, candidate{rel: r, ver: v})
	}
	if len(found) == 0 {
		return Release{}, false, nil
	}
	sort.Slice(found, func(i, j int) bool {
		return found[i].ver.Compare(found[j].ver) < 0
	})
	return found[len(found)-1].rel, true, nil
}

// checkForgeAddress is the check every address this package fetches from is put
// through. The release metadata names the host each file comes from and nothing
// signs that, so a file is only fetched from the forge this build is set to, and
// over https. Plain http is allowed for a forge on the machine itself, which is
// not reachable by anyone else and which is what this package's own tests stand
// up.
func checkForgeAddress(base, raw string) error {
	b, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("the forge address %q cannot be read: %w", base, err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not an address: %w", raw, err)
	}
	if !strings.EqualFold(u.Host, b.Host) {
		return fmt.Errorf("%s is on %s, and this build is set to the forge at %s: nothing is fetched from there", raw, u.Host, b.Host)
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%s is fetched over %q, and this program downloads releases over https", raw, u.Scheme)
	}
	return nil
}

// isLoopbackHost is whether an address names this machine, which is the one
// place a download over plain http is not a download anyone can rewrite.
func isLoopbackHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// newHTTPClient is this package's own client.
//
// http.DefaultClient is a variable any package of the program can replace, and
// a download that follows a redirect to another host is a download from someone
// else, so both are pinned here.
//
// The proxy is off as well, and that is a deliberate cost: HTTP_PROXY and
// HTTPS_PROXY are set by whatever starts the program, and a machine told to send
// its updates through one sends them through a host the release does not name. A
// network that needs a proxy for everything else therefore has no update check,
// rather than a check someone else can answer for.
func newHTTPClient() *http.Client {
	return &http.Client{
		Transport:     newTransport(),
		CheckRedirect: checkForgeRedirect,
	}
}

// newTransport is the default transport with the proxy switched off: the rest of
// it is kept, so the certificate roots, the TLS defaults and the connection
// handling are the ones the standard library gives every other request.
func newTransport() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{Proxy: nil}
	}
	plain := t.Clone()
	plain.Proxy = nil
	return plain
}

// downloadClient is the client the files of a release are fetched with, for the
// two fetches made from functions rather than from a Client. It carries the same
// policy as the client above: this package's own transport, no proxy, and a
// redirect that may not leave the host it was asked for.
var downloadClient = newHTTPClient()

// checkForgeRedirect keeps a download on the host and the scheme it was asked
// for: a release that answers with an address elsewhere is not the release that
// was asked, whatever it then says.
func checkForgeRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("the download was sent on more than twice")
	}
	first := via[0].URL
	if !strings.EqualFold(req.URL.Host, first.Host) || req.URL.Scheme != first.Scheme {
		return fmt.Errorf("the download of %s was sent on to %s, which is somewhere else", first, req.URL)
	}
	return nil
}
