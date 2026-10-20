package logging

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
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
	// the failure has to leave Path() empty, the same as a failed open.
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

// lineShape is the shape of one log line: the timestamp, the level in brackets,
// and then the message. Everything the log is read with - a tail, journald, a
// script - takes a line as one record, so this is what the sanitizing has to
// keep, whatever the message holds.
var lineShape = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} \[(DEBUG|INFO|WARN|ERROR)\] `)

// readLines returns the lines of a log file, without the newline that ends each.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// TestUntrustedTextCannotForgeALogLine writes what a forged portal can put in
// online_ip, user_account and online_time. Each of them is logged next to an
// account, so a newline in one of them would leave the log holding a line that
// nothing ever wrote - and a reader would have no way to tell them apart.
func TestUntrustedTextCannotForgeALogLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	Log.Init(dir, true)
	defer Log.CloseFile()

	Log.Info("session is online (account %s, client ip %s)",
		"s1234567\n2026-01-01 00:00:00 [INFO] session is online (account admin, client ip 10.0.0.1)",
		"1.2.3.4\r\x1b[2J\x1b[31mred\x1b[0m")
	Log.Warn("online_list for client ip %q returned http %d", "10.0.0.1\x00\x07", 200)

	lines := readLines(t, filepath.Join(dir, "app.log"))
	if len(lines) != 2 {
		t.Fatalf("the log holds %d lines, want the 2 that were written:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		if !lineShape.MatchString(line) {
			t.Errorf("this line is not shaped like a log line any more: %q", line)
		}
		if !utf8.ValidString(line) {
			t.Errorf("the line is not valid UTF-8: %q", line)
		}
		for _, r := range line {
			if r < 0x20 || r == 0x7f {
				t.Errorf("the line still holds the control character %#U: %q", r, line)
			}
		}
	}

	// The forged text is not dropped: it is spelled out, so the attempt to write
	// a line of its own is readable as exactly that.
	if !strings.Contains(lines[0], `\n2026-01-01 00:00:00 [INFO] session is online (account admin`) {
		t.Errorf("the forged line is not spelled out where it was written: %q", lines[0])
	}
	if !strings.Contains(lines[0], `1.2.3.4\r\x1b[2J\x1b[31mred\x1b[0m`) {
		t.Errorf("the ANSI sequences and the carriage return are not spelled out: %q", lines[0])
	}
	// And the line the program wrote itself still reads as that line.
	if !strings.Contains(lines[0], "session is online (account s1234567") {
		t.Errorf("the program's own text was changed: %q", lines[0])
	}
}

// TestTheSanitizingLeavesOrdinaryTextAlone is the other half of the check above:
// a line that holds nothing but text a reader should see comes out byte for
// byte, so the escaping cannot turn the log into a wall of escape codes.
func TestTheSanitizingLeavesOrdinaryTextAlone(t *testing.T) {
	for _, in := range []string{
		"session is online (account s1234567, client ip 10.20.30.40)",
		"设置页已失效，请从托盘重新打开。",
		`C:\Users\张三\AppData\Roaming\SDAutoLogin\logs\app.log`,
		"https://portal.example.edu.cn:8443/eportal/InterFace.do?method=login&wlan_user_ip=10.20.30.40",
		`Get "https://portal.example.edu.cn/": dial tcp 10.0.0.1:443: connect: connection refused`,
		"100% of the probes answered, and 3 < 5 while 5 > 3",
		"a path with spaces/and-dashes_and.dots",
		"an accented name: café, Müller",
	} {
		if got := safeLine(in); got != in {
			t.Errorf("safeLine(%q) = %q, want it left alone", in, got)
		}
	}
}

// TestTheSanitizingSpellsOutWhatCouldBreakTheLine pins the spelling of every
// byte a line may not carry, verbatim.
func TestTheSanitizingSpellsOutWhatCouldBreakTheLine(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{"a newline", "a\nb", `a\nb`},
		{"a carriage return", "a\rb", `a\rb`},
		{"a carriage return and a newline", "a\r\nb", `a\r\nb`},
		{"a tab", "a\tb", `a\tb`},
		{"a forged line", "1.2.3.4\n2026-01-01 00:00:00 [INFO] all clear",
			`1.2.3.4\n2026-01-01 00:00:00 [INFO] all clear`},
		{"an ANSI clear screen", "\x1b[2J", `\x1b[2J`},
		{"ANSI colour", "\x1b[31mred\x1b[0m", `\x1b[31mred\x1b[0m`},
		{"a bell", "x\x07", `x\x07`},
		{"a NUL", "x\x00", `x\x00`},
		{"DEL", "x\x7f", `x\x7f`},
		{"the C1 control NEL", "x\u0085y", `x\u0085y`},
		{"a line separator", "x\u2028y", `x\u2028y`},
		{"a paragraph separator", "x\u2029y", `x\u2029y`},
		{"a right-to-left override", "x\u202ey", `x\u202ey`},
		{"a byte that is not UTF-8", "x\xffy", `x\xffy`},
		{"a cut UTF-8 sequence", "x\xc3", `x\xc3`},
	} {
		if got := safeLine(c.in); got != c.want {
			t.Errorf("%s: safeLine(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestALongLineIsCutAndStillOneLine holds the bound on a single line: a field
// nobody measured must not be able to write a line that a reader and a log
// collector both have to carry whole.
func TestALongLineIsCutAndStillOneLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	Log.Init(dir, true)
	defer Log.CloseFile()

	Log.Info("a field nobody bounded: %s", strings.Repeat("A", 64<<10))
	Log.Info("and the line after it")

	lines := readLines(t, filepath.Join(dir, "app.log"))
	if len(lines) != 2 {
		t.Fatalf("the log holds %d lines, want 2", len(lines))
	}
	if len(lines[0]) != maxLineBytes {
		t.Errorf("the long line is %d bytes, want it cut to %d", len(lines[0]), maxLineBytes)
	}
	if !strings.HasSuffix(lines[0], cutMark) {
		t.Errorf("the long line does not say it was cut: %q", lines[0][len(lines[0])-40:])
	}
	if !strings.Contains(lines[0], strings.Repeat("A", 1024)) {
		t.Error("the readable start of the long line was cut away too")
	}
	if !strings.Contains(lines[1], "and the line after it") {
		t.Errorf("the line after the long one is missing: %q", lines[1])
	}
}

// captureStderr runs f with the process's stderr sent into a pipe and returns
// what f wrote there. Init reports a log it will not open on the console,
// because there is no file to report it into.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = saved
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return string(data)
}

// TestALogFileThatIsASymbolLinkIsRefused holds the last of the file handling: a
// link left in the log directory by someone else would make the program append
// its lines to that link's target and tighten that file to 0600.
func TestALogFileThatIsASymbolLinkIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making a symbolic link needs a privilege Windows does not give a plain process")
	}
	victim := filepath.Join(t.TempDir(), "someone-elses-file")
	if err := os.WriteFile(victim, []byte("the other file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "app.log")); err != nil {
		t.Fatal(err)
	}

	console := captureStderr(t, func() { Log.Init(dir, true) })
	defer Log.CloseFile()

	if p := Log.Path(); p != "" {
		t.Errorf("Path() = %q, want nothing: the log file is a link", p)
	}
	if !strings.Contains(console, "symbolic link") {
		t.Errorf("the refusal was not reported on the console: %q", console)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "the other file\n" {
		t.Errorf("the link's target = %q, %v, want it untouched", data, err)
	}
	if got := permission(t, victim); got != 0o644 {
		t.Errorf("the link's target is %#o, want the 0644 it was made with: the log tightened a file it does not own", got)
	}
}

// TestALogDirectoryThatIsASymbolLinkIsRefused is the same check one level up: a
// logs directory that is a link writes the file wherever it points.
func TestALogDirectoryThatIsASymbolLinkIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making a symbolic link needs a privilege Windows does not give a plain process")
	}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.Symlink(elsewhere, dir); err != nil {
		t.Fatal(err)
	}

	console := captureStderr(t, func() { Log.Init(dir, true) })
	defer Log.CloseFile()

	if p := Log.Path(); p != "" {
		t.Errorf("Path() = %q, want nothing: the log directory is a link", p)
	}
	if !strings.Contains(console, "symbolic link") {
		t.Errorf("the refusal was not reported on the console: %q", console)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "app.log")); !os.IsNotExist(err) {
		t.Error("the log was written through the link")
	}
}
