package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Only Windows gets a plain archive. Elsewhere the program arrives in a package
// the package manager owns, so there is nothing here to put in its place.
func TestThePlainArchiveIsTheWindowsOne(t *testing.T) {
	got, ok := ArchiveName("1.3.0", "windows", "amd64")
	if !ok || got != "SDAutoLogin-1.3.0-windows-amd64.zip" {
		t.Fatalf("ArchiveName = %q (ok=%v)", got, ok)
	}
	for _, p := range []struct{ goos, goarch string }{
		{"linux", "amd64"}, {"darwin", "arm64"}, {"freebsd", "amd64"},
		{"windows", "mips"}, {"windows", "riscv64"},
	} {
		if name, ok := ArchiveName("1.3.0", p.goos, p.goarch); ok {
			t.Errorf("%s/%s was given the archive %q", p.goos, p.goarch, name)
		}
	}
}

// A package install is not something the program may write over, and the reason
// it gives is the one the reader has to act on.
func TestAPackageInstallIsNotReplacedByItself(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		got := SelfInstall(Target{GOOS: goos, GOARCH: "amd64", ExePath: "/usr/bin/SDAutoLogin"})
		if got.Possible {
			t.Errorf("%s was told it could replace itself", goos)
		}
		if !strings.Contains(got.Reason, "package manager") {
			t.Errorf("%s: reason = %q", goos, got.Reason)
		}
	}
}

// The directory being writable is not enough: Scoop keeps applications in a
// directory the user owns, and a program that replaced its own file there would
// leave Scoop's record of what it installed describing something else.
func TestACopyAPackageManagerPutThereIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "SDAutoLogin.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Writable, and no marker: this is the case that may replace itself.
	if got := SelfInstall(Target{GOOS: "windows", GOARCH: "amd64", ExePath: exe}); !got.Possible {
		t.Fatalf("a writable directory with no marker was refused: %s", got.Reason)
	}
	// The marker Scoop writes beside what it unpacked.
	if err := os.WriteFile(filepath.Join(dir, "install.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := SelfInstall(Target{GOOS: "windows", GOARCH: "amd64", ExePath: exe})
	if got.Possible {
		t.Error("a copy beside an install.json was allowed to replace itself")
	}
	if !strings.Contains(got.Reason, "Scoop") {
		t.Errorf("reason = %q, and it does not name Scoop", got.Reason)
	}
}

func TestADirectoryThatCannotBeWrittenToIsNamedInTheReason(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the mode bits do not hold here")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "SDAutoLogin.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	got := SelfInstall(Target{GOOS: "windows", GOARCH: "amd64", ExePath: exe})
	if got.Possible {
		t.Fatal("a directory that cannot be written to was accepted")
	}
	if !strings.Contains(got.Reason, dir) {
		t.Errorf("reason = %q, and it does not name the directory", got.Reason)
	}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// The digest comes from the signed list, so this is where a file that is not
// what the release published is stopped.
func TestADownloadThatDoesNotMatchItsDigestIsRefused(t *testing.T) {
	body := []byte("this is the archive")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(srv.Close)

	got, err := Fetch(context.Background(), srv.URL+"/a.zip", sum(body))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Error("Fetch did not return what was served")
	}
	if _, err := Fetch(context.Background(), srv.URL+"/a.zip", sum([]byte("something else"))); err == nil {
		t.Error("a file whose digest did not match was accepted")
	} else if !strings.Contains(err.Error(), "digest") {
		t.Errorf("the refusal does not mention the digest: %v", err)
	}
}

func TestARefusedDownloadIsReportedByStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	_, err := Fetch(context.Background(), srv.URL+"/gone.zip", sum(nil))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("error = %v", err)
	}
}

func windowsArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTheProgramIsReadOutOfTheArchive(t *testing.T) {
	archive := windowsArchive(t, map[string]string{
		"LICENSE":         "licence text",
		"README.md":       "# Campus Auto Login",
		"SDAutoLogin.exe": "the program",
	})
	got, err := UnpackProgram(archive)
	if err != nil {
		t.Fatalf("UnpackProgram: %v", err)
	}
	if string(got) != "the program" {
		t.Errorf("got %q", got)
	}
	// An archive without the program is refused rather than unpacked to
	// nothing.
	if _, err := UnpackProgram(windowsArchive(t, map[string]string{"README.md": "x"})); err == nil {
		t.Error("an archive with no program in it was accepted")
	}
	if _, err := UnpackProgram([]byte("not a zip")); err == nil {
		t.Error("something that is not an archive was accepted")
	}
}

// The copy being replaced moves aside and the new one takes its name, and the
// copy that moved aside is still there to be cleaned up on the next start.
func TestReplacingPutsTheNewProgramInPlace(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "SDAutoLogin.exe")
	if err := os.WriteFile(exe, []byte("old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Replace(exe, []byte("new program")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new program" {
		t.Errorf("the file in place is %q", got)
	}
	aside, err := os.ReadFile(exe + leftoverSuffix)
	if err != nil {
		t.Fatalf("the copy that moved aside is gone: %v", err)
	}
	if string(aside) != "old program" {
		t.Errorf("the copy that moved aside is %q", aside)
	}
	// Nothing of the staging file is left behind.
	matches, _ := filepath.Glob(filepath.Join(dir, "*.new*"))
	if len(matches) != 0 {
		t.Errorf("the staging file was left behind: %v", matches)
	}
	RemoveLeftover(exe)
	if _, err := os.Stat(exe + leftoverSuffix); !os.IsNotExist(err) {
		t.Error("RemoveLeftover did not remove it")
	}
}

// A replacement that cannot happen leaves the program that was there. There is
// no half-replaced state: that would be a program that does not start.
func TestAFailedReplacementLeavesTheProgramThatWasThere(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "SDAutoLogin.exe")
	if err := os.WriteFile(exe, []byte("old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The name the running copy would move aside to is taken by a directory that
	// is not empty, so the move fails after the new file has been written: the
	// worst moment for it to go wrong.
	blocked := exe + leftoverSuffix
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "in the way"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Replace(exe, []byte("new program"))
	if err == nil {
		t.Fatal("Replace reported success although it could not move the program aside")
	}
	if !strings.Contains(err.Error(), "moved aside") {
		t.Errorf("error = %q, and it does not say which step failed", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("the program that was there is gone: %v", err)
	}
	if string(got) != "old program" {
		t.Errorf("the program in place is %q, want the one that was there", got)
	}
	// The staged file is not left lying next to the program.
	matches, _ := filepath.Glob(filepath.Join(dir, "SDAutoLogin.exe.new*"))
	if len(matches) != 0 {
		t.Errorf("the staging file was left behind: %v", matches)
	}
}
