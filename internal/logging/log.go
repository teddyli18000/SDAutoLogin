// Package logging writes the program's log to stderr and to a rotating file. It
// depends on nothing else in the program, so every package can log without an
// import cycle.
package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// maxLogSize is the size at which app.log is rotated into app.log.1.
const maxLogSize = 4 << 20

// maxLineBytes caps one rendered line, the timestamp and the level included. A
// log line is what a reader and a log collector both take as one record, so a
// field that came off the network must not be able to grow one without a bound;
// 8 KiB is far past every line this program writes itself.
const maxLineBytes = 8 << 10

// cutMark is written where a line was cut short, so a line that was cut is not
// read as a line that simply ended.
const cutMark = "...(truncated)"

// The modes the log is made with. The log names the account and the addresses it
// was signed in from, which is private on a machine that several people use, and
// it sits in a directory of its own for the same reason.
const (
	logDirMode  = 0o700
	logFileMode = 0o600
)

// Logger appends timestamped lines to stderr and, unless it was told not to,
// to logs/app.log. DEBUG lines are only written when verbose mode is on, see
// SetVerbose. The console copy goes to stderr, so a command's stdout carries
// only its result and a script can read the status JSON without filtering it.
type Logger struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	size int64
	// rotateDone records that rotation was given up after the rename failed, so
	// it is not retried per line. See rotateLocked.
	rotateDone bool
	verbose    atomic.Bool
}

var Log = &Logger{}

// Init opens the log. dir is where app.log lives, and toFile false keeps the log
// on the console alone for a machine with a small flash: its syslog already keeps
// what the program writes to stderr, so a file would be the same text twice.
func (l *Logger) Init(dir string, toFile bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// A second Init replaces the first one, so the file it opened is closed.
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	// The directory is only remembered when there is a file to open in it: Path
	// tells the tray and the settings page whether there is a log file to show,
	// and a directory nothing was written into would answer that there is one.
	l.dir = ""
	// Rotation is given up for the rest of a run, so a second Init clears that
	// and gives the new file a fresh chance at it.
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
	// A logs directory that is a link writes the file wherever it points, mode
	// 0700 or not, so it is refused before anything is opened inside it.
	if err := refuseSymlink(dir); err != nil {
		_, _ = os.Stderr.WriteString(logLine("ERROR", "cannot use the log directory %s: %v", dir, err) + "\n")
		return
	}
	l.dir = dir
	if err := l.openLocked(); err != nil {
		// Saying nothing would leave Path() pointing at a file that was never
		// opened, and the tray, the log tail and the About tab would name it.
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

// SetVerbose turns the DEBUG lines on or off.
func (l *Logger) SetVerbose(on bool) { l.verbose.Store(on) }

// Verbose reports whether the DEBUG lines are being written.
func (l *Logger) Verbose() bool { return l.verbose.Load() }

// openLocked opens app.log and hands the failure back to its caller, which is
// the only one that can report it: while the mutex is held, write would
// deadlock, so the failure is put on the console directly instead.
func (l *Logger) openLocked() error {
	name := filepath.Join(l.dir, "app.log")
	// A log that is a link is one someone else put there: appending through it
	// would write our lines into their target and the chmod below would tighten
	// that file. Lstat names the link itself rather than what it points at, so
	// this catches a link that was in place before the program started; the
	// check after the open catches one that appeared in between.
	if err := refuseSymlink(name); err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if link, lerr := os.Lstat(name); lerr != nil || !os.SameFile(st, link) {
		_ = f.Close()
		return fmt.Errorf("%s was replaced while it was being opened", name)
	}
	// OpenFile only applies the mode when it creates the file, so a file an
	// earlier version left readable by everyone is tightened here as well. The
	// mode is set on the open handle, not on the name, which by now could have
	// been swapped for a link to a file this program does not own.
	_ = f.Chmod(logFileMode)
	l.file = f
	l.size = st.Size()
	return nil
}

// refuseSymlink reports the path as unusable when it is a symbolic link, or when
// it cannot be examined at all. A path that is not there is fine: the caller
// creates it.
func refuseSymlink(name string) error {
	info, err := os.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("it is a symbolic link")
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

// logLine renders one timestamped line and makes it fit to write. It is separate
// from write so the paths that hold the mutex can put a line on the console
// without going through write, and the sanitizing is here rather than there so
// that every caller goes through it.
func logLine(level, format string, args ...any) string {
	return safeLine(time.Now().Format("2006-01-02 15:04:05.000") + " [" + level + "] " + fmt.Sprintf(format, args...))
}

// safeLine makes one rendered line safe to write, whatever ends up in it. Text a
// reader should see - English, Chinese, paths, URLs, error messages - is kept
// byte for byte; only the bytes that could end the line, drive the terminal or
// hide what is there are turned into their Go spelling: a newline becomes the
// two characters \n, an escape byte becomes \x1b, and a control character that
// is not ASCII becomes \u2028. Nothing is dropped, so a line someone tried to
// forge stays readable as the attempt it was, and an ordinary line comes out
// unchanged. The portal filters what it understands as well, see internal/portal;
// this is the last line of defence, since every package writes through here.
func safeLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			i++
			switch {
			case c == '\n':
				b.WriteString(`\n`)
			case c == '\r':
				b.WriteString(`\r`)
			case c == '\t':
				b.WriteString(`\t`)
			case c < 0x20 || c == 0x7f:
				writeHexEscape(&b, c)
			default:
				b.WriteByte(c)
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			// A byte that is not UTF-8 at all. It is spelled out rather than
			// handed on as a replacement character, which would lose it.
			writeHexEscape(&b, c)
		case breaksLine(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return capLine(b.String())
}

// breaksLine reports the runes that are not text: the control characters, the
// two line separators, and the direction marks that could make a line read as
// one that was never written.
func breaksLine(r rune) bool {
	if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
		return true
	}
	switch r {
	case '\u200e', '\u200f', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	}
	return false
}

// writeHexEscape spells one byte that is not printable the way Go spells it. The
// four characters are plain text, so the byte stops being one that a terminal or
// a log reader acts on.
func writeHexEscape(b *strings.Builder, c byte) {
	const hex = "0123456789abcdef"
	b.WriteString(`\x`)
	b.WriteByte(hex[c>>4])
	b.WriteByte(hex[c&0x0f])
}

// capLine cuts a line that is past maxLineBytes back to it, at a character
// boundary, and marks the cut so it is not read as the end of the text.
func capLine(s string) string {
	if len(s) <= maxLineBytes {
		return s
	}
	cut := maxLineBytes - len(cutMark)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + cutMark
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
// starts a small file again. A failed rename leaves the log growing and gives
// rotation up for the rest of the process: on Windows the rename fails while
// another program holds app.log open, as the tray's "open log" menu does.
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
		// The size stays at the file's real size, so the next line keeps growing
		// the log.
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
