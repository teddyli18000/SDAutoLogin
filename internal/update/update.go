package update

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// listTimeout and sumsTimeout bound the two small requests a check makes. They
// are separate from the archives, which take minutes.
const (
	listTimeout = 30 * time.Second
	sumsTimeout = 30 * time.Second
	// maxSmallBytes bounds the checksum list and its signature: the list is
	// about 1.5 KiB and the signature under 100 bytes, so anything near this is
	// not one of them.
	maxSmallBytes = 64 << 10
)

// Check is what one look for a newer release found.
type Check struct {
	// Current is the version this program is, and Latest the version it could
	// move to. Latest is empty when there is nothing newer.
	Current string
	Latest  string
	Channel Channel
	// Newer is whether a release this channel allows is ahead of this program.
	Newer bool
	// Release is what was found, for the notes and the assets.
	Release Release
	// Install is whether this copy could put the release in place. A check on a
	// package install finds the release and cannot install it, which is the
	// ordinary answer there and not a failure.
	Install Install
	// Signed is whether this build can check what it downloads. It is false
	// until a signing key has been made, and nothing is installed while it is.
	Signed bool
}

// Describe is the one line the log and the interface show.
//
// Whether there is anything newer is read first. A build that cannot install
// what it finds still has to say what it found: with the missing key first, a
// build carrying no key announced the version it had as the newest release
// while the release it had just found went unmentioned.
func (c Check) Describe() string {
	switch {
	case !c.Newer:
		return fmt.Sprintf("%s is the newest release on the %s channel", c.Current, c.Channel)
	case !c.Signed:
		return fmt.Sprintf("%s is available; this build carries no signing key, so it will not install it", c.Latest)
	case !c.Install.Possible:
		return fmt.Sprintf("%s is available, and this copy cannot install it: %s", c.Latest, c.Install.Reason)
	default:
		return fmt.Sprintf("%s is available", c.Latest)
	}
}

// Check looks for a newer release on a channel. It reads and reports; nothing
// is downloaded or installed here.
func (c *Client) Check(ctx context.Context, ch Channel, current Version, t Target) (Check, error) {
	_, err := PublicKeyBytes()
	return c.check(ctx, ch, current, t, err == nil)
}

// check is Check with the state of the signing key named, so a test can drive
// the whole chain with a key of its own.
func (c *Client) check(ctx context.Context, ch Channel, current Version, t Target, signed bool) (Check, error) {
	out := Check{
		Current: current.Tag(),
		Channel: ch,
		Install: SelfInstall(t),
		Signed:  signed,
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	rel, ok, err := c.Newest(ctx, ch, current)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, nil
	}
	out.Newer = true
	out.Release = rel
	out.Latest = rel.Tag
	return out, nil
}

// BuildVersion is the version this build is, in the spelling of a tag without
// its v. The linker writes it from the tag the tree was at, next to the version
// the program reports, since this package cannot read the one internal/app
// holds. A build that was not linked with it does not know whether a release
// moves it forwards, and it installs nothing rather than guess.
var BuildVersion string

// RunningVersion is the version of the copy that is running.
func RunningVersion() (Version, error) {
	text := strings.TrimSpace(BuildVersion)
	if text == "" {
		return Version{}, errors.New("this build was not linked with the version it is, so it cannot tell whether a release is newer")
	}
	v, err := ParseVersion(text)
	if err != nil {
		return Version{}, fmt.Errorf("this build was linked with a version it cannot read: %w", err)
	}
	return v, nil
}

// Install downloads the release a check found and puts it in place of the
// program at exePath. It returns the path of the new program.
//
// The order matters and is the whole of the security here: the signature over
// SHA256SUMS is checked first, so the list of digests is known to be the one
// this project published; the signed list then names the archive to install and
// the version that archive is, so neither the version a release claims nor the
// file it offers can be taken on trust. Nothing is written over the program
// until all of that holds, and never to a version older than the one in place.
func (c *Client) Install(parent context.Context, rel Release, t Target) (string, error) {
	pub, err := PublicKeyBytes()
	if err != nil {
		return "", err
	}
	current, err := RunningVersion()
	if err != nil {
		return "", err
	}
	return c.install(parent, rel, t, pub, current)
}

// install is Install with the key and the version in place named, so a test can
// sign a release with a key of its own and say what the copy being replaced is.
func (c *Client) install(parent context.Context, rel Release, t Target, pub ed25519.PublicKey, current Version) (string, error) {
	install := SelfInstall(t)
	if !install.Possible {
		return "", fmt.Errorf("%s", install.Reason)
	}
	if current.Tag() == "" {
		return "", errors.New("no version is in place to hold the release against")
	}
	// Nothing signs the tag, so it is not what decides which file is installed.
	// It is read for the version it claims, and that version is held against the
	// signed list below.
	declared, err := ParseVersion(rel.Tag)
	if err != nil {
		return "", fmt.Errorf("the release does not name a version: %w", err)
	}

	sumsAsset, ok := rel.Find("SHA256SUMS")
	if !ok {
		return "", fmt.Errorf("the release carries no SHA256SUMS")
	}
	sigAsset, ok := rel.Find("SHA256SUMS" + SignatureSuffix)
	if !ok {
		return "", fmt.Errorf("the release carries no SHA256SUMS%s, so there is nothing to check it against", SignatureSuffix)
	}

	// The two small files are read under one deadline of their own. The archive
	// below is fetched from the context the caller gave, which is not this one:
	// a cancelled deadline here would otherwise stop a download that has
	// minutes of its own to run.
	//
	// Every address is checked first: the release metadata names the host each
	// file comes from, and nothing signs that.
	for _, asset := range []Asset{sumsAsset, sigAsset} {
		if err := checkForgeAddress(c.Base, asset.URL); err != nil {
			return "", err
		}
	}
	sumsCtx, cancel := context.WithTimeout(parent, sumsTimeout)
	sums, err := fetchSmall(sumsCtx, sumsAsset.URL)
	if err != nil {
		cancel()
		return "", err
	}
	sig, err := fetchSmall(sumsCtx, sigAsset.URL)
	cancel()
	if err != nil {
		return "", err
	}
	if err := VerifySignature(pub, sums, string(sig)); err != nil {
		return "", fmt.Errorf("the release is not signed by this project: %w", err)
	}

	sumsList, err := ParseChecksums(sums)
	if err != nil {
		return "", err
	}

	// The name in the signed list is what decides which file is installed: it is
	// covered by the signature, while the name the tag would give is not. The
	// version in it is held against the version the release claims and against
	// the copy in place, so a release can neither label itself a version the
	// signed list does not name nor move a machine backwards onto an older one.
	name, listed, err := archiveIn(sumsList, t.GOOS, t.GOARCH)
	if err != nil {
		return "", err
	}
	if listed.Compare(declared) != 0 {
		return "", fmt.Errorf("the release is labelled %s, and the signed list names %s: the file and the version do not agree", rel.Tag, name)
	}
	if !listed.NewerThan(current) {
		return "", fmt.Errorf("the signed list is for %s and %s is in place: an update never goes backwards", name, current.Tag())
	}
	want, ok := sumsList.Sum(name)
	if !ok {
		return "", fmt.Errorf("the signed list does not name %s", name)
	}
	asset, ok := rel.Find(name)
	if !ok {
		return "", fmt.Errorf("the release carries no %s", name)
	}
	if err := checkForgeAddress(c.Base, asset.URL); err != nil {
		return "", err
	}

	archive, err := Fetch(parent, asset.URL, want)
	if err != nil {
		return "", err
	}
	program, err := UnpackProgram(archive)
	if err != nil {
		return "", err
	}
	if err := Replace(t.ExePath, program); err != nil {
		return "", err
	}
	return t.ExePath, nil
}

// fetchSmall reads one of the two small files a release carries: the checksum
// list and its signature.
func fetchSmall(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered http %d", path.Base(url), resp.StatusCode)
	}
	// Far more than either file holds, and a wrong address does not get to fill
	// memory. Hitting the bound is a failure rather than a short read.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSmallBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSmallBytes {
		return nil, fmt.Errorf("%s is larger than either of the two files it could be", path.Base(url))
	}
	return data, nil
}
