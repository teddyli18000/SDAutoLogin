package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxLogSize = 1 << 20

// Logger appends timestamped lines to stdout and logs/app.log, rotating at 1 MiB.
type Logger struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	size int64
}

var Log = &Logger{}

func (l *Logger) Init(dir string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dir = dir
	_ = os.MkdirAll(dir, 0o755)
	l.openLocked()
}

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
	line := time.Now().Format("2006-01-02 15:04:05") + " [" + level + "] " + fmt.Sprintf(format, args...)
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

func (l *Logger) Info(format string, args ...any)  { l.write("INFO", format, args...) }
func (l *Logger) Warn(format string, args ...any)  { l.write("WARN", format, args...) }
func (l *Logger) Error(format string, args ...any) { l.write("ERROR", format, args...) }
