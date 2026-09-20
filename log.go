package main

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

// Logger appends timestamped lines to stdout and logs/app.log. DEBUG lines are
// only written when verbose mode is on, see SetVerbose.
type Logger struct {
	mu      sync.Mutex
	dir     string
	file    *os.File
	size    int64
	verbose atomic.Bool
}

var Log = &Logger{}

func (l *Logger) Init(dir string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dir = dir
	_ = os.MkdirAll(dir, 0o755)
	l.openLocked()
}

// SetVerbose enables or disables DEBUG output.
func (l *Logger) SetVerbose(on bool) { l.verbose.Store(on) }

// Verbose reports whether DEBUG output is currently enabled.
func (l *Logger) Verbose() bool { return l.verbose.Load() }

func (l *Logger) openLocked() {
	f, err := os.OpenFile(filepath.Join(l.dir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
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
	_, _ = os.Stdout.WriteString(line + "\n")

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
