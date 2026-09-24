//go:build linux || darwin

package platform

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"sdautologin/internal/logging"
)

// instanceLock is held for the lifetime of the process. The kernel drops the
// lock when the process ends, so a crash cannot leave a stale lock behind.
// Keeping a reference to the file also keeps it from being closed by the
// garbage collector, which would release the lock while the program runs.
var instanceLock *os.File

// settingsListener is the socket a later copy connects to in order to ask this
// one for its settings page. It is held for the lifetime of the process, and
// AcquireSingleInstance sets it before WaitForSettingsRequest reads it.
var settingsListener net.Listener

// instanceTaken remembers that this process already holds the lock. A second
// lock request on a fresh descriptor fails even inside one process, so asking
// twice would wrongly report another instance.
var instanceTaken bool

// settingsRequestTimeout bounds how long one connection may take to say what it
// wants, so a connection that says nothing at all cannot hold the loop.
const settingsRequestTimeout = 5 * time.Second

// instanceDir is the directory the two rendezvous names live in: the temporary
// directory, where a second copy looks as well, falling back to the user's cache
// directory where nothing can be written there, a service with a read-only /tmp
// being the usual case, both copies running as the same user.
//
// It is decided once, so that the socket and the lock cannot end up in two
// different directories, one of which the other copy never looks in.
var (
	instanceDirOnce sync.Once
	instanceDirPath string
)

func instanceDir() string {
	instanceDirOnce.Do(func() {
		instanceDirPath = os.TempDir()
		if dirIsWritable(instanceDirPath) {
			return
		}
		if cache, err := os.UserCacheDir(); err == nil && dirIsWritable(cache) {
			instanceDirPath = cache
		}
	})
	return instanceDirPath
}

// dirIsWritable reports whether a file can be made in dir, which is what the lock
// file needs. It is probed rather than read off the mode bits, since a mount option
// or a policy the mode does not show is the usual thing that makes it unusable.
func dirIsWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".sdautologin-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// lockPath and socketPath are the two rendezvous names, next to each other in
// the directory above. The lock decides who is the single copy; the socket is how
// a later copy asks that copy for its settings page.
func lockPath() string   { return filepath.Join(instanceDir(), "sdautologin.lock") }
func socketPath() string { return filepath.Join(instanceDir(), "sdautologin.sock") }

// AcquireSingleInstance takes an exclusive lock on a file in the directory above,
// and publishes a socket beside it that a later copy connects to in order to ask
// for the settings page.
//
// The socket is made before the lock is taken on purpose: a copy that finds the
// lock taken is then certain to find the socket as well, so its request cannot
// be lost in the moment between the two.
func AcquireSingleInstance(requestSettings bool) bool {
	if instanceTaken {
		return true
	}

	listener, listenErr := listenSettingsSocket(socketPath())

	f, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// Nothing to take the lock with, so this copy cannot tell whether another
		// is running. Starting anyway is the lesser evil: refusing would leave the
		// program unusable where both candidate directories are closed off, as a
		// container often has them, though two copies would both be signing in.
		logging.Log.Warn("could not open the lock file %s: %v; this run cannot tell whether another instance is already running",
			lockPath(), err)
		closeListener(listener)
		return true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		logging.Log.Info("another instance is already running, not starting a second one")
		if requestSettings {
			requestSettingsPage(socketPath())
		}
		closeListener(listener)
		_ = f.Close()
		return false
	}

	instanceLock = f
	instanceTaken = true
	settingsListener = listener
	if listener == nil {
		// The lock is ours, so a later copy would talk to this one, and there
		// is no socket for it to talk through. Say so rather than leaving the
		// request unanswered in silence.
		logging.Log.Warn("could not listen for settings requests on %s: %v", socketPath(), listenErr)
	}
	return true
}

// listenSettingsSocket binds the socket at path. An address already bound and
// answering belongs to the running copy and is not ours to take; one that answers
// nothing is a file a crash left behind, removed and made again since nothing else
// is going to.
func listenSettingsSocket(path string) (net.Listener, error) {
	listener, err := net.Listen("unix", path)
	if err == nil {
		return listener, nil
	}
	if conn, derr := net.Dial("unix", path); derr == nil {
		// Someone answers it, so a copy is serving it.
		_ = conn.Close()
		return nil, err
	}
	if rerr := os.Remove(path); rerr != nil {
		return nil, err
	}
	return net.Listen("unix", path)
}

// requestSettingsPage hands the request to the copy listening at path. It is
// best effort: the page is a convenience, and the copy that asked is about to
// exit, so a failure is logged rather than acted on.
func requestSettingsPage(path string) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		logging.Log.Warn("the running instance cannot be asked for its settings page: %v", err)
		return
	}
	logging.Log.Info("asking the running instance to open its settings page")
	defer conn.Close()
	_, _ = conn.Write([]byte{1})
}

// WaitForSettingsRequest calls f for every request a later copy makes. It blocks
// on its own goroutine and returns only if listening fails.
func WaitForSettingsRequest(f func()) {
	if settingsListener == nil {
		return
	}
	serveSettingsRequests(settingsListener, f)
}

// serveSettingsRequests answers requests on listener until it fails.
func serveSettingsRequests(listener net.Listener, f func()) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			logging.Log.Warn("waiting for a settings request failed: %v", err)
			return
		}
		if readSettingsRequest(conn) {
			f()
		}
	}
}

// readSettingsRequest reports whether a connection carried a request. A copy
// probes this socket before it knows whether the copy it wants is there, which is
// how a leftover socket file is told apart from one a running copy answers, so a
// connection that carries nothing is a probe and not a request.
func readSettingsRequest(conn net.Conn) bool {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(settingsRequestTimeout))
	buf := make([]byte, 1)
	n, _ := conn.Read(buf)
	return n > 0
}

func closeListener(listener net.Listener) {
	if listener != nil {
		_ = listener.Close()
	}
}
