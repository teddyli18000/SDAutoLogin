package update

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releaseStub serves a whole release the way the forge serves one: the list of
// releases, the checksum list, its signature, and the archive. Everything the
// install reads comes from here, so the test drives the same chain a reader
// would.
type releaseStub struct {
	tag     string
	prerel  bool
	archive []byte
	sums    []byte
	sig     []byte
	// OmitSig leaves the signature off the release, which is what a release
	// published before signing existed looks like.
	OmitSig bool
	// ArchiveBody overrides what the archive address serves, so a download that
	// is not what the signed list describes can be served.
	ArchiveBody []byte
}

func newReleaseStub(t *testing.T, s *releaseStub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var base string
	asset := func(w http.ResponseWriter, path string) {
		switch path {
		case "/SHA256SUMS":
			w.Write(s.sums)
		case "/SHA256SUMS.sig":
			w.Write(s.sig)
		case "/archive.zip":
			if s.ArchiveBody != nil {
				w.Write(s.ArchiveBody)
				return
			}
			w.Write(s.archive)
		}
	}
	mux.HandleFunc("/api/v1/repos/zqlan/SDAutoLogin/releases", func(w http.ResponseWriter, r *http.Request) {
		assets := []map[string]any{
			{"name": "SHA256SUMS", "size": len(s.sums), "browser_download_url": base + "/SHA256SUMS"},
			{"name": "SDAutoLogin-" + strings.TrimPrefix(s.tag, "v") + "-windows-amd64.zip",
				"size": len(s.archive), "browser_download_url": base + "/archive.zip"},
		}
		if !s.OmitSig {
			assets = append(assets, map[string]any{
				"name": "SHA256SUMS.sig", "size": len(s.sig), "browser_download_url": base + "/SHA256SUMS.sig"})
		}
		out, err := json.Marshal([]map[string]any{{
			"tag_name": s.tag, "prerelease": s.prerel, "draft": false,
			"published_at": "2026-10-07T00:00:00Z", "assets": assets,
		}})
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(out)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { asset(w, r.URL.Path) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL

	// The URLs in the release JSON are built from the address the server ended
	// up on, so the handler is asked once with the base already known.
	return srv
}

// signedRelease builds a release whose checksum list is signed with priv.
func signedRelease(t *testing.T, tag, program string, priv ed25519.PrivateKey) *releaseStub {
	t.Helper()
	archive := windowsArchive(t, map[string]string{
		"README.md":       "# Campus Auto Login",
		"SDAutoLogin.exe": program,
	})
	name := "SDAutoLogin-" + strings.TrimPrefix(tag, "v") + "-windows-amd64.zip"
	sums := []byte(sum(archive) + "  " + name + "\n")
	return &releaseStub{tag: tag, archive: archive, sums: sums, sig: []byte(Sign(priv, sums))}
}

// installTarget is a Windows copy in a directory of its own, which is the shape
// that may replace itself.
func installTarget(t *testing.T, body string) Target {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "SDAutoLogin.exe")
	if err := os.WriteFile(exe, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return Target{GOOS: "windows", GOARCH: "amd64", ExePath: exe}
}

// The whole chain: a release is found, its checksum list is checked against the
// key this build carries, the archive is checked against that list, and the
// program is replaced.
func TestASignedReleaseIsFoundCheckedAndInstalled(t *testing.T) {
	pub, priv := testKey(t)
	rel := signedRelease(t, "v1.3.0", "the new program", priv)
	srv := newReleaseStub(t, rel)
	client := NewClient(srv.URL, "zqlan/SDAutoLogin")
	target := installTarget(t, "the old program")

	got, err := client.check(context.Background(), Stable, mustParse(t, "v1.2.0"), target, true)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !got.Newer || got.Latest != "v1.3.0" {
		t.Fatalf("check found %+v", got)
	}
	if !got.Install.Possible {
		t.Fatalf("this target should be able to replace itself: %s", got.Install.Reason)
	}

	path, err := client.install(context.Background(), got.Release, target, pub, mustParse(t, "v1.2.0"))
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "the new program" {
		t.Errorf("the program in place is %q", body)
	}
}

// The signature is what makes the checksum list trustworthy, and the list is
// what makes the archive trustworthy. A release signed with a key this build
// does not carry is refused before anything is downloaded.
func TestAReleaseSignedWithAnotherKeyIsRefused(t *testing.T) {
	pub, _ := testKey(t)
	_, otherPriv := testKey(t)
	rel := signedRelease(t, "v1.3.0", "the new program", otherPriv)
	srv := newReleaseStub(t, rel)
	target := installTarget(t, "the old program")

	_, err := NewClient(srv.URL, "zqlan/SDAutoLogin").install(context.Background(), Release{
		Tag: rel.tag,
		Assets: []Asset{
			{Name: "SHA256SUMS", URL: srv.URL + "/SHA256SUMS"},
			{Name: "SHA256SUMS.sig", URL: srv.URL + "/SHA256SUMS.sig"},
			{Name: "SDAutoLogin-1.3.0-windows-amd64.zip", URL: srv.URL + "/archive.zip"},
		},
	}, target, pub, mustParse(t, "v1.2.0"))
	if err == nil {
		t.Fatal("a release signed with another key was installed")
	}
	if !strings.Contains(err.Error(), "not signed by this project") {
		t.Errorf("error = %q", err)
	}
	assertUnchanged(t, target.ExePath, "the old program")
}

// An archive that is not the one the signed list describes is refused, which is
// the case the digest exists for: the server hands over something else.
func TestAnArchiveThatIsNotTheSignedOneIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	rel := signedRelease(t, "v1.3.0", "the new program", priv)
	// The address serves a different archive from the one that was summed.
	rel.ArchiveBody = windowsArchive(t, map[string]string{"SDAutoLogin.exe": "something else entirely"})
	srv := newReleaseStub(t, rel)
	target := installTarget(t, "the old program")

	_, err := NewClient(srv.URL, "zqlan/SDAutoLogin").install(context.Background(), Release{
		Tag: rel.tag,
		Assets: []Asset{
			{Name: "SHA256SUMS", URL: srv.URL + "/SHA256SUMS"},
			{Name: "SHA256SUMS.sig", URL: srv.URL + "/SHA256SUMS.sig"},
			{Name: "SDAutoLogin-1.3.0-windows-amd64.zip", URL: srv.URL + "/archive.zip"},
		},
	}, target, pub, mustParse(t, "v1.2.0"))
	if err == nil {
		t.Fatal("an archive that did not match the signed digest was installed")
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Errorf("error = %q, and it does not mention the digest", err)
	}
	assertUnchanged(t, target.ExePath, "the old program")
}

// A release with no signature has nothing to check it against, so it is refused
// rather than installed on trust.
func TestAReleaseWithNoSignatureIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	rel := signedRelease(t, "v1.3.0", "the new program", priv)
	rel.OmitSig = true
	srv := newReleaseStub(t, rel)
	target := installTarget(t, "the old program")

	_, err := NewClient(srv.URL, "zqlan/SDAutoLogin").install(context.Background(), Release{
		Tag: rel.tag,
		Assets: []Asset{
			{Name: "SHA256SUMS", URL: srv.URL + "/SHA256SUMS"},
			{Name: "SDAutoLogin-1.3.0-windows-amd64.zip", URL: srv.URL + "/archive.zip"},
		},
	}, target, pub, mustParse(t, "v1.2.0"))
	if err == nil {
		t.Fatal("a release with no signature was installed")
	}
	if !strings.Contains(err.Error(), "SHA256SUMS.sig") {
		t.Errorf("error = %q", err)
	}
	assertUnchanged(t, target.ExePath, "the old program")
}

// A package install is told what it is, and nothing is downloaded for it.
func TestAPackageInstallIsNotOfferedAnInstall(t *testing.T) {
	_, priv := testKey(t)
	rel := signedRelease(t, "v2.0.0", "the new program", priv)
	srv := newReleaseStub(t, rel)

	got, err := NewClient(srv.URL, "zqlan/SDAutoLogin").check(context.Background(), Stable,
		mustParse(t, "v1.2.0"), Target{GOOS: "linux", GOARCH: "amd64", ExePath: "/usr/bin/SDAutoLogin"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Newer {
		t.Error("the newer release was not found")
	}
	if got.Install.Possible {
		t.Error("a package install was told it could replace itself")
	}
	if !strings.Contains(got.Describe(), "package manager") {
		t.Errorf("the line shown says %q", got.Describe())
	}
}

// A build with no key made yet says so and does not offer to install, which is
// the state this program is in until keygen has been run.
func TestABuildWithNoKeySaysSoAndInstallsNothing(t *testing.T) {
	_, priv := testKey(t)
	rel := signedRelease(t, "v2.0.0", "the new program", priv)
	srv := newReleaseStub(t, rel)

	// The state is named and not read from the build, so this keeps covering it
	// after a key has been made.
	got, err := NewClient(srv.URL, "zqlan/SDAutoLogin").check(context.Background(), Stable,
		mustParse(t, "v1.2.0"), installTarget(t, "the old program"), false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Signed {
		t.Error("a build with no key reported that it was signed")
	}
	if !strings.Contains(got.Describe(), "no signing key") {
		t.Errorf("the line shown says %q", got.Describe())
	}
}

func assertUnchanged(t *testing.T, path, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the program that was there is gone: %v", err)
	}
	if string(body) != want {
		t.Errorf("the program in place is %q, want %q", body, want)
	}
}

// A build that carries no key still has to report what it found. Reading the
// missing key before the question of whether anything is newer made a keyless
// build announce the version it had as the newest release while the release it
// had just found went unmentioned.
func TestAKeylessBuildStillSaysWhatItFound(t *testing.T) {
	line := Check{
		Current: "1.3.0-alpha.1",
		Channel: Stable,
		Newer:   true,
		Latest:  "v2.0.0",
		Signed:  false,
	}.Describe()
	if !strings.Contains(line, "v2.0.0") {
		t.Errorf("the line does not name the release that was found: %q", line)
	}
	if strings.Contains(line, "is the newest release") {
		t.Errorf("the line calls the version in hand the newest: %q", line)
	}

	// And with nothing newer, it says so, key or no key.
	line = Check{Current: "1.3.0-alpha.1", Channel: Stable}.Describe()
	if !strings.Contains(line, "is the newest release") {
		t.Errorf("the line does not say there is nothing newer: %q", line)
	}
}

// releaseOf is a release as the client reads it out of the list: the assets the
// install asks for, with the addresses it fetches them from.
func releaseOf(s *releaseStub, base string) Release {
	tag := strings.TrimPrefix(s.tag, "v")
	return Release{
		Tag: s.tag,
		Assets: []Asset{
			{Name: "SHA256SUMS", URL: base + "/SHA256SUMS"},
			{Name: "SHA256SUMS.sig", URL: base + "/SHA256SUMS.sig"},
			{Name: "SDAutoLogin-" + tag + "-windows-amd64.zip", URL: base + "/archive.zip"},
		},
	}
}

// Rotating the signing key is a hand operation, and this is what it costs.
//
// A build carries one key, so the build made after the rotation refuses the
// releases the old key signed, and a copy still carrying the old key refuses the
// releases the new one signs. The second is the one that matters: a copy
// installed before the rotation cannot reach a release made after it, and the
// reader updates that one by hand. Every refusal names both keys, so a rotation
// can be told from an attempt to hand the reader something else.
func TestRotatingTheKeyLeavesTheCopiesBuiltBeforeItBehind(t *testing.T) {
	oldPub, oldPriv := testKey(t)
	newPub, newPriv := testKey(t)

	// The last release before the rotation, and the first one after it.
	beforeRelease := signedRelease(t, "v2.0.0", "the program the old key signed", oldPriv)
	afterRelease := signedRelease(t, "v2.1.0", "the program the new key signed", newPriv)
	before := newReleaseStub(t, beforeRelease)
	after := newReleaseStub(t, afterRelease)

	// The build carrying the new key takes the release the new key signed.
	target := installTarget(t, "the program in place")
	if _, err := NewClient(after.URL, "zqlan/SDAutoLogin").install(
		context.Background(), releaseOf(afterRelease, after.URL), target, newPub, mustParse(t, "v2.0.0")); err != nil {
		t.Fatalf("the build carrying the new key refused the new release: %v", err)
	}

	// And refuses the one the old key signed, which is what stops a release
	// signed with a key nobody uses any more from being installed.
	target = installTarget(t, "the program in place")
	_, err := NewClient(before.URL, "zqlan/SDAutoLogin").install(
		context.Background(), releaseOf(beforeRelease, before.URL), target, newPub, mustParse(t, "v1.9.0"))
	if err == nil {
		t.Fatal("the build carrying the new key installed a release signed with the old one")
	}
	if !strings.Contains(err.Error(), KeyID(oldPub)) || !strings.Contains(err.Error(), KeyID(newPub)) {
		t.Errorf("the refusal does not name both keys: %v", err)
	}
	assertUnchanged(t, target.ExePath, "the program in place")

	// The copy built before the rotation is the one that has to be updated by
	// hand: it carries the old key and cannot check anything the new one signs.
	target = installTarget(t, "the copy built before the rotation")
	_, err = NewClient(after.URL, "zqlan/SDAutoLogin").install(
		context.Background(), releaseOf(afterRelease, after.URL), target, oldPub, mustParse(t, "v2.0.0"))
	if err == nil {
		t.Fatal("a copy carrying the old key installed a release signed with the new one")
	}
	if !strings.Contains(err.Error(), KeyID(oldPub)) || !strings.Contains(err.Error(), KeyID(newPub)) {
		t.Errorf("the refusal does not name both keys: %v", err)
	}
	assertUnchanged(t, target.ExePath, "the copy built before the rotation")
}
