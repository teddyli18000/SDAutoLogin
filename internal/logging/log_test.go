package logging

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInitWithAFileWritesAndCloses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")

	Log.Init(dir, true)
	Log.Info("the first line")
	path := Log.Path()
	if path == "" {
		t.Fatal("Path() is empty, want the file that is being written")
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "the first line") {
		t.Fatalf("app.log = %q, %v", data, err)
	}

	// The configuration can turn the file off after the log was opened, since
	// the log has to exist before the configuration can be read: a failure to
	// read it has to be logged too.
	Log.CloseFile()
	Log.Info("the second line")
	if p := Log.Path(); p != "" {
		t.Errorf("Path() = %q after CloseFile, want nothing", p)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "the second line") {
		t.Error("CloseFile kept writing to the file")
	}
}

func TestInitWithoutAFileKeepsTheLogOnTheConsole(t *testing.T) {
	// A machine with a small flash asks for this: its service manager already
	// collects the console output, so the file would be the same text twice.
	dir := filepath.Join(t.TempDir(), "logs")

	Log.Init(dir, false)
	Log.Info("this must not reach the disk")

	if p := Log.Path(); p != "" {
		t.Errorf("Path() = %q, want nothing, since no file is written", p)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the log directory was created: %v", err)
	}
}

func TestTheLogAndItsDirectoryBelongToTheirOwner(t *testing.T) {
	// The log names the account and the addresses it was signed in from, so it
	// is for the user the program runs as. Windows has no permission bits of its
	// own: Go reports 0666 for a writable file there whatever the ACL says.
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are a Unix thing")
	}
	defer Log.CloseFile()

	dir := filepath.Join(t.TempDir(), "logs")

	Log.Init(dir, true)
	Log.Info("a line")

	file := filepath.Join(dir, "app.log")
	if got := permission(t, dir); got != 0o700 {
		t.Errorf("the log directory is %#o, want 0700", got)
	}
	if got := permission(t, file); got != 0o600 {
		t.Errorf("the log file is %#o, want 0600", got)
	}

	// A file an earlier version left readable by everyone is tightened when the
	// log is opened again; see openLocked.
	Log.CloseFile()
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	Log.Init(dir, true)
	if got := permission(t, file); got != 0o600 {
		t.Errorf("a log file that was readable by everyone is %#o after Init, want 0600", got)
	}
}

// permission is the permission bits of a file or a directory.
func permission(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestASecondInitReplacesTheFirst(t *testing.T) {
	first := filepath.Join(t.TempDir(), "logs")
	second := filepath.Join(t.TempDir(), "logs")
	// Windows will not let the temporary directory go while the file in it is
	// open, hence closing the log at the end.
	defer Log.CloseFile()

	Log.Init(first, true)
	Log.Info("to the first")
	Log.Init(second, true)
	Log.Info("to the second")

	if data, _ := os.ReadFile(filepath.Join(first, "app.log")); strings.Contains(string(data), "to the second") {
		t.Error("the first file kept being written after a second Init")
	}
	if data, _ := os.ReadFile(filepath.Join(second, "app.log")); !strings.Contains(string(data), "to the second") {
		t.Errorf("the second file was not written: %q", data)
	}
}

func TestInitDoesNotReportAFileItCouldNotOpen(t *testing.T) {
	// A directory where app.log belongs makes the open fail, and Path() must not
	// then name a file that was never opened.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "app.log"), 0o700); err != nil {
		t.Fatal(err)
	}

	Log.Init(dir, true)
	defer Log.CloseFile()

	if p := Log.Path(); p != "" {
		t.Errorf("Path() = %q, want nothing after the log file could not be opened", p)
	}
}

func TestInitDoesNotReportADirectoryItCouldNotCreate(t *testing.T) {
	// A regular file where the log directory belongs makes MkdirAll fail, and
	// the failure has to leave Path() empty just as a failed open does.
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	Log.Init(filepath.Join(blocker, "logs"), true)
	defer Log.CloseFile()

	if p := Log.Path(); p != "" {
		t.Errorf("Path() = %q, want nothing when the directory could not be created", p)
	}
}

func TestAFailedRotationIsNotRetriedForEveryLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A full app.log so the next line asks for a rotation...
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxLogSize)), 0o600); err != nil {
		t.Fatal(err)
	}
	// ...and a directory at the rotation target, which makes the rename fail
	// whatever the platform, the way a file another program holds open does on
	// Windows.
	if err := os.Mkdir(filepath.Join(dir, "app.log.1"), 0o700); err != nil {
		t.Fatal(err)
	}

	Log.Init(dir, true)
	defer Log.CloseFile()

	// The first line fills the log past the limit and cannot rotate it.
	Log.Info("the line that asks for a rotation")

	Log.mu.Lock()
	gaveUp := Log.rotateDone
	handle := Log.file
	size := Log.size
	Log.mu.Unlock()

	if !gaveUp {
		t.Fatal("rotation was not marked as given up after the rename failed")
	}
	if handle == nil {
		t.Fatal("no handle was taken back, so the log would stop being written")
	}
	if size <= maxLogSize {
		t.Errorf("size = %d, want more than %d, since the file was not truncated", size, maxLogSize)
	}
	// The filled file is still there, so nothing was silently truncated.
	if info, err := os.Stat(path); err != nil || info.Size() <= maxLogSize {
		t.Fatalf("app.log = %v, %v, want the full file left in place", info, err)
	}

	// Following lines must not repeat the close and rename: the handle stays
	// the same and the size keeps growing.
	Log.Info("a line after the failed rotation")
	Log.mu.Lock()
	sameHandle := Log.file == handle
	grew := Log.size > size
	Log.mu.Unlock()
	if !sameHandle {
		t.Error("a later line reopened the file, so the failed rotation was retried per line")
	}
	if !grew {
		t.Error("the log stopped growing, so the failed rotation reset the size")
	}
}
