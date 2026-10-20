//go:build linux || darwin

package platform

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"sdautologin/internal/logging"
)

// lockFileName and socketFileName are the two rendezvous names, next to each
// other in a directory only this user can write to (see instanceDirCandidates).
// The lock decides who is the single copy; the socket is how a later copy asks
// that copy for its settings page.
const (
	lockFileName   = "sdautologin.lock"
	socketFileName = "sdautologin.sock"
)

// instanceLock is held for the lifetime of the process. The kernel drops the
// lock when the process ends, so a crash leaves no stale lock behind, and the
// file reference keeps the collector from closing it early.
var instanceLock *os.File

// settingsListener is the socket a later copy connects to ask this one for its
// settings page. It is held for the lifetime of the process;
// AcquireSingleInstance sets it before WaitForSettingsRequest reads it.
var settingsListener net.Listener

// instanceTaken remembers that this process already holds the lock: a second
// request on a fresh descriptor fails even inside one process, so asking twice
// would wrongly report another instance.
var instanceTaken bool

// settingsRequestTimeout bounds how long one connection may take to say what it
// wants, so a silent connection cannot hold the loop.
const settingsRequestTimeout = 5 * time.Second

// settingsConnectPoll is how often a copy that lost the lock looks for the
// winner's socket. The winner takes the lock a few instructions before it binds,
// so the wait only has to cover a scheduling gap.
const settingsConnectPoll = 20 * time.Millisecond

// settingsConnectWait is how long it keeps looking. A winner whose bind failed
// says so in its own log, so waiting past a moment would only delay the copy that
// is on its way out. It is a variable so a test does not have to spend it.
var settingsConnectWait = 500 * time.Millisecond

// settingsPeerUID is the uid a connection to the settings socket has to come
// from. It is read on the goroutine that answers the socket and set once here,
// but a test names another uid to watch a connection of its own be turned away -
// the only way to see the check decide anything on a machine where every process
// runs as one user - so it is written from a second goroutine as well and has to
// be an atomic.
var settingsPeerUID atomic.Uint32

func init() { settingsPeerUID.Store(uint32(os.Getuid())) }

// instanceDir is the directory the two rendezvous names live in: the first of
// the candidates below that this run can use. It is decided once, for both
// names.
var (
	instanceDirOnce sync.Once
	instanceDirPath string
)

func instanceDir() string {
	instanceDirOnce.Do(func() {
		if dirs := instanceDirCandidates(); len(dirs) > 0 {
			instanceDirPath = dirs[0]
		}
	})
	return instanceDirPath
}

// instanceDirCandidates lists the directories the rendezvous names may live in,
// best first. Not one of them is a shared directory such as /tmp: every account
// on the machine can write there, so any of them can put a lock file at the name
// first, hold it, and have every copy started afterwards believe another copy is
// already running. The sticky bit of such a directory then keeps the owner of
// the program from clearing the file. A unit with PrivateTmp=yes is the second
// reason: the service had a /tmp of its own, so the lock it took and the lock a
// copy started by hand took were two different files and both copies ran.
func instanceDirCandidates() []string {
	var dirs []string

	// A unit with StateDirectory= makes systemd name one directory that both the
	// service and a copy started by hand see, and the units ask for it, so it is
	// preferred when it is there.
	if state := stateDirectory(); state != "" {
		dirs = append(dirs, state)
	}
	// XDG_RUNTIME_DIR is the directory the session keeps for this user alone:
	// owned by the user, 0700, and thrown away when the session ends. A lock and
	// a socket of this kind belong nowhere else.
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); privateDir(runtimeDir, false) {
		dirs = append(dirs, runtimeDir)
	}
	// A run no session started, from cron or from a shell with no
	// XDG_RUNTIME_DIR, has only the user's own directories left.
	if cache, err := os.UserCacheDir(); err == nil && privateDir(cache, true) {
		dirs = append(dirs, cache)
	}
	if config, err := os.UserConfigDir(); err == nil && privateDir(config, false) {
		dirs = append(dirs, config)
	}
	return dirs
}

// stateDirectory is the directory systemd made for the service, or "" when this
// run is not one systemd started with StateDirectory=. A unit may name several,
// separated by colons, and the first writable one is the one the lock goes in.
func stateDirectory() string {
	for _, dir := range strings.Split(os.Getenv("STATE_DIRECTORY"), ":") {
		if dir != "" && dirIsWritable(dir) {
			return dir
		}
	}
	return ""
}

// privateDir reports whether dir is a directory only this user can write to:
// owned by this user, with no write bit for the group or for anyone else, and
// writable in fact rather than only in its mode bits. A name in a directory that
// answers yes cannot have been put there by another account, which is the whole
// point of moving the rendezvous names out of a shared one. create makes the
// directory when it is not there, which the cache directory of a home that has
// never run an XDG program is not.
func privateDir(dir string, create bool) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil {
		if !create || !os.IsNotExist(err) {
			return false
		}
		if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
			return false
		}
		if info, err = os.Stat(dir); err != nil {
			return false
		}
	}
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return false
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return false
	}
	return dirIsWritable(dir)
}

// dirIsWritable reports whether a file can be made in dir, the one thing the lock
// file needs. The mode bits are not enough: a mount option or a policy they do
// not show is the usual reason a directory cannot be written.
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

// rendezvousPath is one of the two names in the directory above. An empty result
// means this run has no directory only it can write to, which is refused rather
// than answered with a name in a shared one.
func rendezvousPath(name string) string {
	dir := instanceDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// lockPath and socketPath are the two rendezvous names, next to each other in the
// directory above.
func lockPath() string   { return rendezvousPath(lockFileName) }
func socketPath() string { return rendezvousPath(socketFileName) }

// AcquireSingleInstance takes an exclusive lock on a file in the directory above
// and, having taken it, publishes a socket beside it.
//
// The lock is taken first, and only the copy that holds it binds the socket. A
// copy that has lost must not bind one: the bind would fail against the winner's
// socket, and the recovery from a failed bind is to unlink the name and make it
// again - which takes the address away from a winner whose dial was refused for
// a moment, leaving it listening where nothing can find it, while the copy that
// asked exits. Binding first also let a loser answer its own request for the
// page, which is the same thing seen from the reader's side: nothing opens.
func AcquireSingleInstance(requestSettings bool) bool {
	if instanceTaken {
		return true
	}

	lock, listener, ours := acquireSingleInstance(requestSettings)
	if !ours {
		return false
	}
	instanceLock = lock
	instanceTaken = true
	settingsListener = listener
	return true
}

// acquireSingleInstance takes the lock in the first candidate directory it can and
// hands back the socket a later copy talks to.
//
// A directory whose lock file cannot be opened, or that turns out to hold
// something other than the lock file this program wrote, is passed over for the
// next candidate rather than run without a lock: a copy that cannot tell itself
// from a second one leaves two copies fighting over the same config.json and the
// same log, which is the state the lock exists to prevent. It is only when no
// candidate works at all that this run refuses to start, and it says so.
func acquireSingleInstance(requestSettings bool) (*os.File, net.Listener, bool) {
	dirs := instanceDirCandidates()
	if len(dirs) == 0 {
		logging.Log.Error("no directory only this user can write to was found for the lock of the single instance; refusing to start, since a second copy could not be told apart from this one")
		return nil, nil, false
	}
	for _, dir := range dirs {
		lock, listener, ours := acquireInstance(filepath.Join(dir, lockFileName), filepath.Join(dir, socketFileName), requestSettings)
		if lock != nil || !ours {
			return lock, listener, ours
		}
	}
	logging.Log.Error("the lock of the single instance could not be taken in any of %v; refusing to start, since a second copy could not be told apart from this one", dirs)
	return nil, nil, false
}

// acquireInstance is the order and the two rendezvous names, with the paths
// handed in so a test can work in a directory of its own. It returns the lock
// this copy holds (nil when there was none to take), the socket it answers on
// (nil when there is none), and whether this copy is the single one.
//
// O_EXCL is deliberately left out of the open below. The lock file is meant to
// stay behind for the next run to find and lock, so a create that refused a file
// already there would force an unlink-then-create dance, and the moment between
// the two is one in which another file can be put at the name. O_NOFOLLOW and the
// checks in openLockFile settle the same question without that moment.
func acquireInstance(lockPath, socketPath string, requestSettings bool) (*os.File, net.Listener, bool) {
	f, err := openLockFile(lockPath)
	if err != nil {
		// This path is no use - a directory that turned out to be read-only, a
		// name someone else put something else at - so this copy cannot tell
		// whether another one is running. The caller tries the next candidate
		// directory rather than carry on without a lock.
		logging.Log.Warn("could not take the lock file %s: %v; another directory has to be tried", lockPath, err)
		return nil, nil, true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		logging.Log.Info("another instance is already running, not starting a second one")
		if requestSettings {
			requestSettingsPage(socketPath)
		}
		_ = f.Close()
		return nil, nil, false
	}

	listener, listenErr := listenSettingsSocket(socketPath)
	if listener == nil {
		// The lock is ours, so a later copy would talk to this one, and there is
		// no socket for it to talk through.
		logging.Log.Warn("could not listen for settings requests on %s: %v", socketPath, listenErr)
	}
	return f, listener, true
}

// openLockFile opens the lock file at path, making it when it is not there yet,
// and checks what it opened rather than trusting the name. O_NOFOLLOW makes the
// open itself fail on a symbolic link, so a link someone left at the name cannot
// point this run at a file of their choosing, and what does come back is
// required to be a regular file owned by this user with no write bit for the
// group or for anyone else.
func openLockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || int(st.Uid) != os.Getuid() || info.Mode().Perm()&0o022 != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("%s is not a lock file this run may use: it has to be a regular file owned by uid %d that the group and everyone else may read but not write", path, os.Getuid())
	}
	return f, nil
}

// listenSettingsSocket binds the socket at path. An address already bound and
// answering belongs to the running copy; one that answers nothing is a file a
// crash left behind, removed and made again.
func listenSettingsSocket(path string) (net.Listener, error) {
	if err := checkSocketName(path); err != nil {
		return nil, err
	}
	listener, err := listenUnixSocket(path)
	if err != nil {
		if conn, derr := net.Dial("unix", path); derr == nil {
			_ = conn.Close()
			return nil, err
		}
		if rerr := os.Remove(path); rerr != nil {
			return nil, err
		}
		return listenUnixSocket(path)
	}
	return listener, nil
}

// checkSocketName refuses a name that is a symbolic link, since everything done
// with the name afterwards - probing it, unlinking it, binding over it - would be
// done to whatever the link points at. A leftover that is not a link is left
// alone here, so that a copy which is serving the name keeps it. Anything at the
// name that belongs to another user is refused for the same reason.
func checkSocketName(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link, not the socket this program made", path)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to uid %d, not to this user (%d)", path, st.Uid, os.Getuid())
	}
	return nil
}

// listenUnixSocket binds the socket and takes the write bits off it. bind(2)
// makes a socket with the umask of the session and the net package never chmods
// it, so a loose umask used to leave a socket that another account could connect
// to. A chmod that fails is a warning and not a reason to give the socket up: it
// sits in a directory only this user can write to, where the mode decides
// nothing.
func listenUnixSocket(path string) (net.Listener, error) {
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		logging.Log.Warn("could not take the write bit off the settings socket %s: %v", path, err)
	}
	return listener, nil
}

// requestSettingsPage hands the request to the copy listening at path, trying
// for a moment first. The lock is taken before the socket is made, so a copy
// that starts in the same instant can be the single one and not yet be
// listening; a reader who asked for the window must not lose it to that
// handful of microseconds. It is best effort either way: the page is a
// convenience and the copy that asked is about to exit, so a failure is only
// logged.
func requestSettingsPage(path string) {
	deadline := time.Now().Add(settingsConnectWait)
	for {
		err := askForSettingsPage(path)
		if err == nil {
			logging.Log.Info("asking the running instance to open its settings page")
			return
		}
		if !time.Now().Before(deadline) {
			logging.Log.Warn("the running instance cannot be asked for its settings page: %v", err)
			return
		}
		time.Sleep(settingsConnectPoll)
	}
}

// askForSettingsPage is one attempt at handing the request over. A connection
// that carries nothing is a probe, not a request, so nothing is written until
// the socket answered.
func askForSettingsPage(path string) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte{1})
	return err
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
// probes this socket before it knows whether the copy it wants is there, so a
// connection that carries nothing is a probe, not a request. The uid of the
// process at the other end has to be this user's as well: the socket is a way
// into a page holding an account, and the kernel is the only one that can say
// who is on the other end of it.
func readSettingsRequest(conn net.Conn) bool {
	defer conn.Close()
	if !peerIsThisUser(conn) {
		logging.Log.Warn("refusing a settings request on %s from another user", conn.RemoteAddr())
		return false
	}
	_ = conn.SetReadDeadline(time.Now().Add(settingsRequestTimeout))
	buf := make([]byte, 1)
	n, _ := conn.Read(buf)
	return n > 0
}

// peerIsThisUser reports whether the process at the other end of a unix socket
// connection runs as this user, asking the kernel rather than the peer. A peer
// that cannot be checked at all is refused: this is the one check that keeps a
// connection from another account from opening the settings page.
func peerIsThisUser(conn net.Conn) bool {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false
	}
	var uid uint32
	var known bool
	if err := raw.Control(func(fd uintptr) { uid, known = socketPeerUID(fd) }); err != nil {
		return false
	}
	if !known {
		logging.Log.Warn("the kernel would not name the user at the other end of a settings connection; refusing it")
		return false
	}
	return uid == settingsPeerUID.Load()
}

// peerCredLevelOption is the level and the option name the peer's credentials are
// asked for under. The two systems disagree on both - Linux reports them at
// SOL_SOCKET/SO_PEERCRED, macOS at SOL_LOCAL/LOCAL_PEERCRED - and the values are
// written out rather than borrowed from a constant that only one of the two
// headers has.
func peerCredLevelOption() (level, option uintptr) {
	if runtime.GOOS == "darwin" {
		return 0, 0x001 // SOL_LOCAL, LOCAL_PEERCRED
	}
	return 1, 17 // SOL_SOCKET, SO_PEERCRED
}

// socketPeerUID asks the kernel for the uid of the process at the other end of a
// connected unix socket, the one answer about a peer that it cannot make up. The
// two systems write the uid second either way - Linux struct ucred {pid_t pid;
// uid_t uid; gid_t gid;}, macOS struct xucred {u_int version; uid_t cr_uid;
// short cr_ngroups; gid_t cr_groups[16]} - so it is read at offset 4 in both.
func socketPeerUID(fd uintptr) (uint32, bool) {
	level, option := peerCredLevelOption()
	buf := make([]byte, 128)
	size := uint32(len(buf))
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, level, option,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	if errno != 0 || size < 8 {
		return 0, false
	}
	return binary.NativeEndian.Uint32(buf[4:8]), true
}
