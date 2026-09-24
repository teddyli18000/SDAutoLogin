// Package logging writes the program's log to stderr and to a rotating file. It
// has no dependency on the rest of the program on purpose, so every other
// package can log without an import cycle.
package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// maxLogSize is the size at which app.log is rotated into app.log.1.
const maxLogSize = 4 << 20

// The modes the log is made with. The log names the account and the addresses it
// was signed in from, which is private on a machine that several people use, and
// it sits in a directory of its own for the same reason.
const (
	logDirMode  = 0o700
	logFileMode = 0o600
)

// Logger appends timestamped lines to stderr and, unless it was told not to,
// to logs/app.log. DEBUG lines are only written when verbose mode is on, see
// SetVerbose.
//
// The console copy goes to stderr rather than stdout, so a command's stdout
// carries only its result and a script can read the JSON of the status command
// without filtering the log out first.
type Logger struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	size int64
	// rotateDone records that rotation was given up because the rename failed:
	// retrying it costs a close and a reopen per line, and the rename cannot
	// start working while whatever holds app.log keeps holding it.
	rotateDone bool
	verbose    atomic.Bool
}

var Log = &Logger{}

// Init opens the log. dir is where app.log lives, and toFile false keeps the log
// on the console alone for a machine with a small flash: its syslog already keeps
// whatever the program writes to stderr, and a rotating file on top of that is
// the same text written to the flash twice.
func (l *Logger) Init(dir string, toFile bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// A second Init replaces the first one, so whatever was open is closed
	// rather than left behind.
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	// The directory is only remembered when there is a file to open in it: Path
	// tells the tray and the settings page whether there is a log file to show,
	// and a directory nothing was written into would answer that there is one.
	l.dir = ""
	// A second Init is a fresh chance to rotate: the earlier one may have given
	// up because another program held app.log open at the time.
	l.rotateDone = false
	if !toFile {
		return
	}
	if err := os.MkdirAll(dir, logDirMode); err != nil {
		// The line goes straight to stderr because this holds the mutex write
		// needs, and there is no file to report the failure into anyway.
		_, _ = os.Stderr.WriteString(logLine("ERROR", "cannot create the log directory %s: %v", dir, err) + "\n")
		return
	}
	l.dir = dir
	if err := l.openLocked(); err != nil {
		// Saying nothing here would leave Path() pointing at a file that was
		// never opened, and the tray, the log tail and the About tab would all
		// name a file that is not there.
		_, _ = os.Stderr.WriteString(logLine("ERROR", "cannot open the log file %s: %v", filepath.Join(dir, "app.log"), err) + "\n")
		l.dir = ""
	}
}

// CloseFile stops writing the log to its file. The console copy keeps going: the
// file is the copy that can be turned off, since a machine that has no room for
// it usually has a service manager keeping the console output anyway.
func (l *Logger) CloseFile() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	l.dir = ""
}

// SetVerbose enables or disables DEBUG output.
func (l *Logger) SetVerbose(on bool) { l.verbose.Store(on) }

// Verbose reports whether DEBUG output is currently enabled.
func (l *Logger) Verbose() bool { return l.verbose.Load() }

// openLocked opens app.log and hands the failure back to its caller, which is
// the only one that can report it: while the mutex is held, write would
// deadlock, so the failure is put on the console directly instead.
func (l *Logger) openLocked() error {
	name := filepath.Join(l.dir, "app.log")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
	if err != nil {
		return err
	}
	// OpenFile only applies the mode when it creates the file, so a file an
	// earlier version left readable by everyone is tightened here as well.
	_ = os.Chmod(name, logFileMode)
	l.file = f
	if st, err := f.Stat(); err == nil {
		l.size = st.Size()
	}
	return nil
}

// Path returns the active log file, empty if Init has not run.
func (l *Logger) Path() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dir == "" {
		return ""
	}
	return filepath.Join(l.dir, "app.log")
}

// logLine renders one timestamped line. It is separate from write so the paths
// that hold the mutex can put a line on the console without going through write.
func logLine(level, format string, args ...any) string {
	return time.Now().Format("2006-01-02 15:04:05.000") + " [" + level + "] " + fmt.Sprintf(format, args...)
}

func (l *Logger) write(level, format string, args ...any) {
	line := logLine(level, format, args...)
	_, _ = os.Stderr.WriteString(line + "\n")

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	n, err := l.file.WriteString(line + "\n")
	if err != nil {
		return
	}
	l.size += int64(n)
	if l.size > maxLogSize && !l.rotateDone {
		l.rotateLocked()
	}
}

// rotateLocked moves the full app.log aside to app.log.1 so the next line
// starts a small file again. A rename that fails leaves the log growing
// instead of truncating it, and rotation is then given up for the rest of the
// process: on Windows the rename fails for as long as another program holds
// app.log open — the tray's own "open log" menu hands it to an editor — and
// repeating the close and reopen for every following line would cost that work
// per line without ever succeeding.
func (l *Logger) rotateLocked() {
	name := filepath.Join(l.dir, "app.log")
	// The handle is given up first because Windows refuses to rename an open
	// file; if the rename does not happen it has to be taken back, or the log
	// would fall silent.
	_ = l.file.Close()
	l.file = nil
	if err := os.Rename(name, filepath.Join(l.dir, "app.log.1")); err != nil {
		l.rotateDone = true
		if reopenErr := l.openLocked(); reopenErr != nil {
			l.dir = ""
			_, _ = os.Stderr.WriteString(logLine("ERROR",
				"cannot rotate the log file %s (%v) and cannot reopen it either (%v)", name, err, reopenErr) + "\n")
			return
		}
		// The size is left as the file's real size on purpose, so the next line
		// keeps growing the log rather than restarting at a size that is not
		// true; rotation is off, so the size no longer has to reach the limit.
		_, _ = os.Stderr.WriteString(logLine("ERROR",
			"cannot rotate the log file %s: %v (the log keeps growing and rotation is off until the next Init)", name, err) + "\n")
		return
	}
	l.size = 0
	if err := l.openLocked(); err != nil {
		l.dir = ""
		_, _ = os.Stderr.WriteString(logLine("ERROR", "cannot open the new log file %s: %v", name, err) + "\n")
	}
}

func (l *Logger) Debug(format string, args ...any) {
	if !l.verbose.Load() {
		return
	}
	l.write("DEBUG", format, args...)
}

func (l *Logger) Info(format string, args ...any)  { l.write("INFO", format, args...) }
func (l *Logger) Warn(format string, args ...any)  { l.write("WARN", format, args...) }
func (l *Logger) Error(format string, args ...any) { l.write("ERROR", format, args...) }
