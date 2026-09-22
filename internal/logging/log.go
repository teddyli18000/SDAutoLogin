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
	mu      sync.Mutex
	dir     string
	file    *os.File
	size    int64
	verbose atomic.Bool
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
	if !toFile {
		return
	}
	_ = os.MkdirAll(dir, logDirMode)
	l.dir = dir
	l.openLocked()
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

func (l *Logger) openLocked() {
	name := filepath.Join(l.dir, "app.log")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
	if err != nil {
		return
	}
	// OpenFile only applies the mode when it creates the file, so a file an
	// earlier version left readable by everyone is tightened here as well.
	_ = os.Chmod(name, logFileMode)
	l.file = f
	if st, err := f.Stat(); err == nil {
		l.size = st.Size()
	}
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

func (l *Logger) write(level, format string, args ...any) {
	line := time.Now().Format("2006-01-02 15:04:05.000") + " [" + level + "] " + fmt.Sprintf(format, args...)
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
	if l.size > maxLogSize {
		_ = l.file.Close()
		_ = os.Rename(filepath.Join(l.dir, "app.log"), filepath.Join(l.dir, "app.log.1"))
		l.file = nil
		l.size = 0
		l.openLocked()
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
