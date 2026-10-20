package update

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// someoneElse is a host that is not the one a test's client is set to. Nothing
// is ever asked of it: the address is refused before a request is made.
const someoneElse = "https://someone-elses-host.example"

// The version a release claims is metadata from the forge, and nothing signs it.
// What is signed is the name of the archive, and the version in that name is the
// version of the bytes. A release that labels itself something other than the
// archive it carries is refused.
func TestAReleaseLabelledAnotherVersionThanTheSignedArchiveIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	published := signedRelease(t, "v1.3.0", "the new program", priv)
	srv := newReleaseStub(t, published)
	target := installTarget(t, "the old program")

	// Every file the download touches is the genuine release; only the label the
	// forge describes it with has been rewritten.
	rel := releaseOf(published, srv.URL)
	rel.Tag = "v9.9.9"

	_, err := NewClient(srv.URL, "zqlan/SDAutoLogin").install(context.Background(), rel, target, pub, mustParse(t, "v1.2.0"))
	if err == nil {
		t.Fatal("a release labelled another version than the archive it carries was installed")
	}
	if !strings.Contains(err.Error(), "v9.9.9") || !strings.Contains(err.Error(), "SDAutoLogin-1.3.0-windows-amd64.zip") {
		t.Errorf("error = %q, and it does not name both the label and the signed name", err)
	}
	assertUnchanged(t, target.ExePath, "the old program")
}

// An older release is refused even when it is a genuine, properly signed release
// of this project: nothing about it is forged, and installing it would move the
// machine backwards.
func TestAnOlderSignedReleaseIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	older := signedRelease(t, "v1.2.0", "the older program", priv)
	srv := newReleaseStub(t, older)
	client := NewClient(srv.URL, "zqlan/SDAutoLogin")
	target := installTarget(t, "the program in place")

	_, err := client.install(context.Background(), releaseOf(older, srv.URL), target, pub, mustParse(t, "v1.3.0"))
	if err == nil {
		t.Fatal("an older release was installed over a newer one")
	}
	if !strings.Contains(err.Error(), "never goes backwards") {
		t.Errorf("error = %q, and it does not say why the release was refused", err)
	}
	if !strings.Contains(err.Error(), "1.2.0") || !strings.Contains(err.Error(), "1.3.0") {
		t.Errorf("error = %q, and it does not name both versions", err)
	}
	assertUnchanged(t, target.ExePath, "the program in place")

	// The version in place is not newer than itself either, so a release cannot
	// be installed over the copy that already is it.
	if _, err := client.install(context.Background(), releaseOf(older, srv.URL), target, pub, mustParse(t, "v1.2.0")); err == nil {
		t.Fatal("a release of the version already in place was installed over it")
	}
	assertUnchanged(t, target.ExePath, "the program in place")
}

// A label that is not a version is refused rather than compared on a guess.
func TestAReleaseWhoseLabelIsNotAVersionIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	published := signedRelease(t, "v1.3.0", "the new program", priv)
	srv := newReleaseStub(t, published)
	target := installTarget(t, "the old program")

	rel := releaseOf(published, srv.URL)
	rel.Tag = "nightly-rebuild"
	_, err := NewClient(srv.URL, "zqlan/SDAutoLogin").install(context.Background(), rel, target, pub, mustParse(t, "v1.2.0"))
	if err == nil || !strings.Contains(err.Error(), "does not name a version") {
		t.Fatalf("error = %v, and the label was not refused as one", err)
	}
	assertUnchanged(t, target.ExePath, "the old program")
}

// The address of every file is metadata too. A release that points a download at
// another host is refused before anything is asked of that host.
func TestAnAssetOnAnotherHostIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	published := signedRelease(t, "v1.3.0", "the new program", priv)
	srv := newReleaseStub(t, published)
	target := installTarget(t, "the old program")

	rel := releaseOf(published, srv.URL)
	rel.Assets[0].URL = someoneElse + "/SHA256SUMS"

	_, err := NewClient(srv.URL, "zqlan/SDAutoLogin").install(context.Background(), rel, target, pub, mustParse(t, "v1.2.0"))
	if err == nil {
		t.Fatal("a release that pointed a download at another host was installed")
	}
	if !strings.Contains(err.Error(), "someone-elses-host.example") {
		t.Errorf("error = %q, and it does not name the host that was refused", err)
	}
	assertUnchanged(t, target.ExePath, "the old program")
}

// The archive's address is checked as well, and the file it names is the one the
// signed list names.
func TestAnArchiveAssetOnAnotherHostIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	published := signedRelease(t, "v1.3.0", "the new program", priv)
	srv := newReleaseStub(t, published)
	target := installTarget(t, "the old program")

	rel := releaseOf(published, srv.URL)
	rel.Assets[2].URL = someoneElse + "/archive.zip"

	_, err := NewClient(srv.URL, "zqlan/SDAutoLogin").install(context.Background(), rel, target, pub, mustParse(t, "v1.2.0"))
	if err == nil {
		t.Fatal("an archive served from another host was installed")
	}
	if !strings.Contains(err.Error(), "someone-elses-host.example") {
		t.Errorf("error = %q, and it does not name the host that was refused", err)
	}
	assertUnchanged(t, target.ExePath, "the old program")
}

// The forge this build is set to is https. A release that keeps the host but
// drops the scheme is answered by whoever is on the way, so it is refused.
func TestAPlainHTTPDownloadFromTheForgeHostIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	published := signedRelease(t, "v1.3.0", "the new program", priv)
	target := installTarget(t, "the old program")

	_, err := NewClient("https://git.zqlhub.com", "zqlan/SDAutoLogin").install(
		context.Background(), releaseOf(published, "http://git.zqlhub.com"), target, pub, mustParse(t, "v1.2.0"))
	if err == nil {
		t.Fatal("a release fetched over plain http from the forge host was installed")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error = %q, and it does not say https is required", err)
	}
	assertUnchanged(t, target.ExePath, "the old program")
}

// The instance itself is checked: a build pointed at a forge over plain http is
// one whose release list anyone on the way can write.
func TestAPlainHTTPForgeIsRefusedBeforeItIsAsked(t *testing.T) {
	_, err := NewClient("http://git.zqlhub.com", "zqlan/SDAutoLogin").Releases(context.Background())
	if err == nil {
		t.Fatal("a forge over plain http was asked for its releases")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error = %q, and it does not say https is required", err)
	}
}

// A download that is sent on to another host is not the file that was asked for.
func TestADownloadSentOnToAnotherHostIsRefused(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("the file from somewhere else"))
	}))
	t.Cleanup(elsewhere.Close)
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/a.zip", http.StatusFound)
	}))
	t.Cleanup(start.Close)

	body := []byte("the archive the digest was made from")
	_, err := Fetch(context.Background(), start.URL+"/a.zip", sum(body))
	if err == nil {
		t.Fatal("a download that was sent on to another host was accepted")
	}
	if !strings.Contains(err.Error(), "somewhere else") {
		t.Errorf("error = %q, and it does not say the download went elsewhere", err)
	}
}

// The read of the program out of an archive is bounded, and one that goes past
// the bound is refused rather than cut short into a file that would be put in
// place as a program that does not start.
//
// The bound is tried at a small one rather than at the 128 MiB the program uses:
// it is the same read on the same code path, and the boundary is what matters —
// a program exactly at the bound is read whole, one byte more is refused. The
// constant the program itself reads under is maxArchiveBytes.
func TestAnArchiveHoldingMoreThanTheProgramIsBigIsRefused(t *testing.T) {
	const limit = 64 << 10

	at := streamedArchive(t, progName+".exe", limit)
	program, err := unpackProgram(at, limit)
	if err != nil {
		t.Fatalf("a program exactly at the bound was refused: %v", err)
	}
	if len(program) != limit {
		t.Errorf("the program read is %d bytes, want %d", len(program), limit)
	}

	over := streamedArchive(t, progName+".exe", limit+1)
	if _, err := unpackProgram(over, limit); err == nil {
		t.Fatal("a program one byte past the bound was unpacked")
	} else if !strings.Contains(err.Error(), "larger than this program will read") {
		t.Errorf("error = %q, and it does not say the program is too large", err)
	}
}

// A release list that fills its own bound is refused for the same reason.
func TestAReleaseListLargerThanTheProgramWillReadIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte(" "), maxListBytes+1))
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, "zqlan/SDAutoLogin").Releases(context.Background())
	if err == nil {
		t.Fatal("a release list larger than the bound was read")
	}
	if !strings.Contains(err.Error(), "larger than this program will read") {
		t.Errorf("error = %q, and it does not say the reply is too large", err)
	}
}

// streamedArchive is a zip holding one entry of the given size. The bytes go in
// in chunks of zeros, so the archive on the disk is a few hundred kilobytes
// while the entry inside it is as large as the caller asked for.
func streamedArchive(t *testing.T, name string, size int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for written := 0; written < size; written += len(chunk) {
		n := len(chunk)
		if rest := size - written; rest < n {
			n = rest
		}
		if _, err := w.Write(chunk[:n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The signed list is what decides which archive is installed, so a list that
// names none for this platform, or two, says nothing that can be installed.
func TestASignedListThatDoesNotNameOneArchiveForThePlatformIsRefused(t *testing.T) {
	one := Checksums{
		"SDAutoLogin-1.3.0-windows-amd64.zip": "aa",
		"SDAutoLogin-1.3.0-windows-arm64.zip": "bb",
		"sdautologin_1.3.0-1_amd64.deb":       "cc",
	}
	name, ver, err := archiveIn(one, "windows", "amd64")
	if err != nil || name != "SDAutoLogin-1.3.0-windows-amd64.zip" || ver.Tag() != "1.3.0" {
		t.Fatalf("archiveIn = %q, %q, %v", name, ver.Tag(), err)
	}
	if _, _, err := archiveIn(one, "windows", "386"); err == nil {
		t.Error("a list that names no archive for the platform was accepted")
	} else if !strings.Contains(err.Error(), "no archive") {
		t.Errorf("error = %q", err)
	}
	two := Checksums{
		"SDAutoLogin-1.3.0-windows-amd64.zip": "aa",
		"SDAutoLogin-1.2.0-windows-amd64.zip": "bb",
	}
	if _, _, err := archiveIn(two, "windows", "amd64"); err == nil {
		t.Error("a list that names two archives for the platform was accepted")
	} else if !strings.Contains(err.Error(), "two archives") {
		t.Errorf("error = %q", err)
	}
	if _, _, err := archiveIn(Checksums{"SDAutoLogin-nightly-windows-amd64.zip": "aa"}, "windows", "amd64"); err == nil {
		t.Error("a list naming an archive whose version cannot be read was accepted")
	}
}

// Replace keeps the mode of the program it replaces, since os.CreateTemp writes
// one that is not executable.
func TestReplaceKeepsTheProgramExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the mode bits do not hold here")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "SDAutoLogin")
	if err := os.WriteFile(exe, []byte("the old program"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(exe, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := Replace(exe, []byte("the new program")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("the program in place is %v, want the mode of the one it replaced (0750)", got)
	}
}

// A program whose old copy cannot be read back for its mode still has to be
// executable: the 0600 os.CreateTemp writes is a program that never starts.
func TestTheNewProgramIsExecutableWhenTheOldOneCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the mode bits do not hold here")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "SDAutoLogin")
	// A symbolic link to nowhere: the name is there to be renamed and what it
	// points at is not, so the mode of the copy being replaced cannot be read.
	if err := os.Symlink(filepath.Join(dir, "not there"), exe); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := Replace(exe, []byte("the new program")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatalf("the new program is not there: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("the new program is %v, want 0755: a program has to be runnable", got)
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "the new program" {
		t.Errorf("the file in place is %q", body)
	}
}

// A build that was not linked with its own version installs nothing: it cannot
// tell whether a release moves it forwards, and installing on a guess is how a
// machine ends up moving backwards.
func TestABuildThatDoesNotKnowItsVersionInstallsNothing(t *testing.T) {
	_, priv := testKey(t)
	published := signedRelease(t, "v1.3.0", "the new program", priv)
	srv := newReleaseStub(t, published)
	target := installTarget(t, "the old program")
	rel := releaseOf(published, srv.URL)

	// The version is a variable so that the linker can write it, so a test can
	// stand in for the linker here.
	saved := BuildVersion
	t.Cleanup(func() { BuildVersion = saved })

	BuildVersion = ""
	_, err := NewClient(srv.URL, "zqlan/SDAutoLogin").Install(context.Background(), rel, target)
	if err == nil {
		t.Fatal("a build that does not know its own version installed a release")
	}
	if !strings.Contains(err.Error(), "not linked with the version") {
		t.Errorf("error = %q, and it does not say why nothing was installed", err)
	}
	assertUnchanged(t, target.ExePath, "the old program")

	// Nothing else about it is different: the same release, installed by a build
	// that knows its version, is the case the tests above drive.
	assertUnchanged(t, target.ExePath, "the old program")
}

// The version an install is held against comes from the linker. A build linked
// with nothing, or with something that is not a version, has no version to
// compare a release with.
func TestRunningVersionReadsWhatTheLinkerWrote(t *testing.T) {
	saved := BuildVersion
	t.Cleanup(func() { BuildVersion = saved })

	BuildVersion = ""
	if v, err := RunningVersion(); err == nil {
		t.Errorf("a build with no version reported %q", v.Tag())
	} else if !strings.Contains(err.Error(), "not linked with the version") {
		t.Errorf("error = %q", err)
	}

	BuildVersion = "the last one"
	if _, err := RunningVersion(); err == nil {
		t.Error("a build linked with something that is not a version reported one")
	}

	BuildVersion = "1.3.0"
	v, err := RunningVersion()
	if err != nil || v.Tag() != "1.3.0" {
		t.Errorf("RunningVersion = %q, %v", v.Tag(), err)
	}
}
