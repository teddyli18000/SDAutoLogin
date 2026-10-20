//go:build linux || darwin

package platform

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestTheDirectoryProbeTellsWhatCanBeWritten covers the question the fallback
// rests on. The probe has to be right about three shapes: a directory of its
// own, something that is not a directory, and a path that is not there.
func TestTheDirectoryProbeTellsWhatCanBeWritten(t *testing.T) {
	dir := t.TempDir()
	if !dirIsWritable(dir) {
		t.Errorf("dirIsWritable(%s) = false, want true for a directory of its own", dir)
	}

	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dirIsWritable(file) {
		t.Errorf("dirIsWritable(%s) = true, want false for something that is not a directory", file)
	}
	if dirIsWritable(filepath.Join(dir, "gone", "deeper")) {
		t.Error("dirIsWritable() = true for a directory that is not there, want false")
	}
}

// loserAgainst runs acquireInstance as the copy that loses: the test takes the
// lock first, in a directory of its own, and the copy under test finds it held.
// It returns what that copy published and whether it called itself the single
// one.
func loserAgainst(t *testing.T, sock string, wait time.Duration) (listener net.Listener, ours bool) {
	t.Helper()

	saved := settingsConnectWait
	settingsConnectWait = wait
	t.Cleanup(func() { settingsConnectWait = saved })

	lock := filepath.Join(t.TempDir(), "sdautologin.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(lock, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	// The test stands in for the copy that got there first, so that
	// acquireInstance below is the copy that lost.
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("the test could not take the lock: %v", err)
	}

	got, gotListener, gotOurs := acquireInstance(lock, sock, true)
	if got != nil {
		_ = got.Close()
	}
	return gotListener, gotOurs
}

// TestTheLosingCopyDoesNotBindTheSocket covers the order a single copy is decided
// in. The copy that lost used to make the socket before it knew it had lost, so
// with a winner that had none it answered its own request for the settings page
// and then exited: the reader asked for the window and nothing opened.
func TestTheLosingCopyDoesNotBindTheSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "sdautologin.sock")

	listener, ours := loserAgainst(t, sock, 50*time.Millisecond)
	if ours {
		t.Fatal("a copy that lost the lock called itself the single one")
	}
	if listener != nil {
		t.Fatal("a copy that lost the lock published a socket of its own")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("a copy that lost the lock left a socket behind: %v", err)
	}
}

// TestTheLosingCopyLeavesTheWinnersSocketAlone covers the other half of binding
// first: the recovery from a bind that failed is to unlink the name and make it
// again, so a copy that bound before it knew it had lost could take the address
// away from a winner whose dial was refused for a moment. The winner would then
// be listening where no later copy can find it.
func TestTheLosingCopyLeavesTheWinnersSocketAlone(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "sdautologin.sock")

	winner, err := listenSettingsSocket(sock)
	if err != nil {
		t.Fatalf("listenSettingsSocket: %v", err)
	}
	t.Cleanup(func() { _ = winner.Close() })
	asked := make(chan struct{}, 1)
	go serveSettingsRequests(winner, func() { asked <- struct{}{} })

	listener, ours := loserAgainst(t, sock, 500*time.Millisecond)
	if ours || listener != nil {
		t.Fatal("a copy that lost the lock published a socket of its own")
	}
	// The request reached the winner, and the winner is still the one a later
	// copy talks to.
	select {
	case <-asked:
	case <-time.After(2 * time.Second):
		t.Fatal("the request never reached the copy that holds the lock")
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("the winner's socket is gone: %v", err)
	}
	probe, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("the winner's address no longer takes a connection: %v", err)
	}
	_ = probe.Close()
}

// TestTheRequestWaitsForASocketThatIsNotThereYet covers the window the order
// opens: the winner takes the lock a few instructions before it binds, so a copy
// that lost may look before there is anything to find. The request waits that
// moment out rather than being lost to it.
func TestTheRequestWaitsForASocketThatIsNotThereYet(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "sdautologin.sock")

	asked := make(chan struct{}, 1)
	go func() {
		// The winner, arriving at its bind a moment later.
		time.Sleep(80 * time.Millisecond)
		listener, err := listenSettingsSocket(sock)
		if err != nil {
			return
		}
		defer listener.Close()
		serveSettingsRequests(listener, func() { asked <- struct{}{} })
	}()

	listener, ours := loserAgainst(t, sock, 2*time.Second)
	if ours || listener != nil {
		t.Fatal("a copy that lost the lock published a socket of its own")
	}
	select {
	case <-asked:
	case <-time.After(3 * time.Second):
		t.Fatal("the request was lost to the moment before the socket was bound")
	}
}

// TestASecondCopyCanAskTheRunningOneForItsPage covers the whole path with the
// lock held by a copy that also listens, which is the ordinary state of a running
// program.
func TestASecondCopyCanAskTheRunningOneForItsPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.sock")
	listener, err := listenSettingsSocket(path)
	if err != nil {
		t.Fatalf("listenSettingsSocket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	asked := make(chan struct{}, 2)
	go serveSettingsRequests(listener, func() { asked <- struct{}{} })

	// A copy probes the socket before it knows whether anyone answers it, so a
	// connection that carries nothing must not open a page.
	probe, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("probe dial: %v", err)
	}
	_ = probe.Close()
	select {
	case <-asked:
		t.Fatal("a connection that carried nothing opened the page")
	case <-time.After(100 * time.Millisecond):
	}

	requestSettingsPage(path)

	select {
	case <-asked:
	case <-time.After(2 * time.Second):
		t.Fatal("the request did not reach the copy that was listening")
	}
}

func TestListenSettingsSocketKeepsALiveOneAndReplacesADeadOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.sock")

	// A file that nothing answers is what a crash leaves behind: it is replaced
	// so that this copy can be the one a later one talks to.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("writing the leftover file: %v", err)
	}
	listener, err := listenSettingsSocket(path)
	if err != nil {
		t.Fatalf("listenSettingsSocket over a leftover file: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	// A second call must not take the address away from the copy serving it.
	if stolen, err := listenSettingsSocket(path); err == nil {
		_ = stolen.Close()
		t.Fatal("the address of a copy that is listening was taken")
	}

	// The original listener is still the one that works.
	asked := make(chan struct{}, 1)
	go serveSettingsRequests(listener, func() { asked <- struct{}{} })
	requestSettingsPage(path)
	select {
	case <-asked:
	case <-time.After(2 * time.Second):
		t.Fatal("the original listener no longer answers")
	}
}

// A unit with PrivateTmp=yes gives the service a /tmp of its own, so the lock it
// takes and the lock a copy started by hand takes are two different files and
// both copies run. StateDirectory= is the one directory they both see, and the
// unit asks for it, so it wins over the temporary directory when it is there.
func TestTheStateDirectoryWinsOverTheTemporaryOne(t *testing.T) {
	state := t.TempDir()
	t.Setenv("STATE_DIRECTORY", state)

	// The directory is chosen once for the life of the process, so the choice made
	// by an earlier run of this test - or by any test before it - is cleared here
	// and asked for again. Without that the answer to both questions below is
	// whatever directory was picked first, and a repeated run of the suite fails
	// on a decision this test never got to watch being made.
	instanceDirOnce = sync.Once{}
	instanceDirPath = ""
	t.Cleanup(func() {
		instanceDirOnce = sync.Once{}
		instanceDirPath = ""
	})

	if got := stateDirectory(); got != state {
		t.Errorf("stateDirectory() = %q, want %q", got, state)
	}
	if got := instanceDir(); got != state {
		t.Errorf("instanceDir() = %q, want the state directory %q", got, state)
	}
	if got := filepath.Dir(lockPath()); got != state {
		t.Errorf("the lock is in %q, want it under %q", lockPath(), state)
	}
	if got := filepath.Dir(socketPath()); got != state {
		t.Errorf("the socket is in %q, want it under %q", socketPath(), state)
	}
}

// A run systemd did not start has no state directory, and the temporary
// directory is what it has always used.
func TestNoStateDirectoryFallsBackToTheTemporaryOne(t *testing.T) {
	t.Setenv("STATE_DIRECTORY", "")
	if got := stateDirectory(); got != "" {
		t.Errorf("stateDirectory() = %q, want empty when systemd named none", got)
	}
}

// systemd hands over a list when a unit names more than one directory, and a
// directory that cannot be written is no use for a lock file.
func TestTheStateDirectoryTakesTheFirstOneItCanWrite(t *testing.T) {
	state := t.TempDir()
	t.Setenv("STATE_DIRECTORY", "/proc/nonexistent:"+state+":/also/not/here")
	if got := stateDirectory(); got != state {
		t.Errorf("stateDirectory() = %q, want the first writable one %q", got, state)
	}
}

// The lock and the socket used to live in a directory every account on the
// machine can write to, so any of them could put a lock file at the name first,
// hold it, and leave every copy started afterwards believing another copy was
// already running - with the sticky bit keeping the owner from clearing it. The
// tests below cover where the names are put instead, and what is refused at
// them.

// TestTheSessionRuntimeDirectoryComesBeforeTheUsersOwnDirectories covers the
// order the candidate directories are tried in: the one the session keeps for
// this user alone, then the user's own, and nothing shared at all.
func TestTheSessionRuntimeDirectoryComesBeforeTheUsersOwnDirectories(t *testing.T) {
	session := t.TempDir()
	cache := t.TempDir()
	config := t.TempDir()
	t.Setenv("STATE_DIRECTORY", "")
	t.Setenv("XDG_RUNTIME_DIR", session)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_CONFIG_HOME", config)

	want := []string{session, cache, config}
	if got := instanceDirCandidates(); !slices.Equal(got, want) {
		t.Errorf("instanceDirCandidates() = %q, want %q", got, want)
	}
}

// TestADirectoryAnyoneCanWriteInIsPassedOver covers what the directory is asked
// for: a name in it has to be one this user alone can have put there, so a
// directory the group or anyone else may write into is no better than /tmp was.
func TestADirectoryAnyoneCanWriteInIsPassedOver(t *testing.T) {
	shared := t.TempDir()
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	config := t.TempDir()
	t.Setenv("STATE_DIRECTORY", "")
	t.Setenv("XDG_RUNTIME_DIR", shared)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_CONFIG_HOME", config)

	got := instanceDirCandidates()
	if slices.Contains(got, shared) {
		t.Errorf("instanceDirCandidates() = %q, want the world writable %q left out", got, shared)
	}
	if len(got) == 0 || got[0] != cache {
		t.Errorf("instanceDirCandidates() = %q, want %q first", got, cache)
	}
}

// TestTheUsersOwnCacheDirectoryIsMadeWhenItIsNotThereYet covers the run that no
// session started: no XDG_RUNTIME_DIR to use, and a home directory that has
// never had an XDG program in it. The name still has to land in a directory of
// this user's own, so one is made rather than the lock falling through to a
// shared directory.
func TestTheUsersOwnCacheDirectoryIsMadeWhenItIsNotThereYet(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "cache")
	t.Setenv("STATE_DIRECTORY", "")
	// A relative value is not a path this run can put a name in.
	t.Setenv("XDG_RUNTIME_DIR", "relative")
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)

	got := instanceDirCandidates()
	if len(got) == 0 || got[0] != cache {
		t.Fatalf("instanceDirCandidates() = %q, want %q first", got, cache)
	}
	if !privateDir(cache, false) {
		t.Errorf("%s was not made a directory only this user can write to", cache)
	}
}

// TestNoUsableDirectoryRefusesToStart covers the failure the lock was allowed to
// have before: with nowhere to take the lock, a copy ran anyway and could not
// tell itself from a second one. It refuses now, which is the one answer that
// keeps two copies off the same config.json.
func TestNoUsableDirectoryRefusesToStart(t *testing.T) {
	for _, name := range []string{"STATE_DIRECTORY", "XDG_RUNTIME_DIR", "XDG_CACHE_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(name, "")
	}
	// A home that is not there and cannot be made, so neither the cache nor the
	// config directory is any use.
	t.Setenv("HOME", "/proc/sdautologin-no-such-home")
	t.Cleanup(func() {
		instanceTaken = false
		instanceLock = nil
		settingsListener = nil
	})

	if got := instanceDirCandidates(); len(got) != 0 {
		t.Fatalf("instanceDirCandidates() = %q, want none", got)
	}
	if AcquireSingleInstance(false) {
		t.Error("a copy started although there was no directory to take the lock in")
	}
}

// TestALockFileThatIsASymbolicLinkIsNotFollowed covers the file the lock is taken
// on: the name has to be a file this program made, not a link that points the run
// at a file of someone else's choosing. The file at the other end is held here,
// so a run that followed the link would take its answer from a lock another
// process holds and report another instance.
func TestALockFileThatIsASymbolicLinkIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "somebody-elses-file")
	if err := os.WriteFile(target, []byte("not a lock file"), 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(target, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, lockFileName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	lock, listener, ours := acquireInstance(link, filepath.Join(dir, socketFileName), false)
	if lock != nil {
		_ = lock.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if !ours {
		t.Error("the run followed the link: it reported another instance from the lock held at the other end of it")
	}
	if lock != nil {
		t.Error("a lock file that is a symbolic link was opened")
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symbolic link at %s is gone: %v", link, err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "not a lock file" {
		t.Errorf("the file at the other end of the link was written to: %q, %v", data, err)
	}
}

// TestALockFileAnyoneCanWriteIsRefused covers the other half of what the lock
// name is checked for, and the name that is not a file at all.
func TestALockFileAnyoneCanWriteIsRefused(t *testing.T) {
	dir := t.TempDir()

	open := filepath.Join(dir, "open-lock")
	if err := os.WriteFile(open, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	// The umask may have taken the write bits off at creation, and they are the
	// whole point of the case, so they are put back by hand.
	if err := os.Chmod(open, 0o666); err != nil {
		t.Fatal(err)
	}
	if f, err := openLockFile(open); err == nil {
		_ = f.Close()
		t.Error("a lock file the group and everyone else may write was accepted")
	}

	if err := os.Mkdir(filepath.Join(dir, "a-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if f, err := openLockFile(filepath.Join(dir, "a-directory")); err == nil {
		_ = f.Close()
		t.Error("a directory at the lock name was accepted as a lock file")
	}

	good := filepath.Join(dir, lockFileName)
	f, err := openLockFile(good)
	if err != nil {
		t.Fatalf("openLockFile(%s) = %v, want the lock file this program makes", good, err)
	}
	_ = f.Close()
}

// TestASocketNameThatIsASymbolicLinkIsRefused covers the other rendezvous name.
// Everything done with the socket name - probing it, unlinking it, binding over
// it - would be done to whatever a link at the name points at.
func TestASocketNameThatIsASymbolicLinkIsRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "somebody-elses-file")
	if err := os.WriteFile(target, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, socketFileName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if listener, err := listenSettingsSocket(link); err == nil {
		_ = listener.Close()
		t.Error("a symbolic link was bound over")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep me" {
		t.Errorf("the file at the other end of the link was changed: %q, %v", data, err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symbolic link at %s is gone: %v", link, err)
	}
}

// TestTheSettingsSocketIsMadePrivate covers the mode of the socket itself. bind
// makes it with the session umask alone, so with a umask of 0 - put in place here
// so that the check means something - the socket used to come out world
// writable.
func TestTheSettingsSocketIsMadePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), socketFileName)
	old := syscall.Umask(0)
	defer syscall.Umask(old)

	listener, err := listenSettingsSocket(path)
	if err != nil {
		t.Fatalf("listenSettingsSocket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("the settings socket was made %o, want 600", got)
	}
}

// TestAConnectionFromAnotherUserIsRefused covers the check on the process at the
// other end of a settings connection. A test runs as one user, so the kernel is
// asked for that user's connections either way; naming another uid is the same
// decision the code makes about a connection from another account, and the
// request that goes through once the right uid is named again shows that it was
// the uid and nothing else that turned the first one away.
func TestAConnectionFromAnotherUserIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), socketFileName)
	listener, err := listenSettingsSocket(path)
	if err != nil {
		t.Fatalf("listenSettingsSocket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	asked := make(chan struct{}, 2)
	go serveSettingsRequests(listener, func() { asked <- struct{}{} })

	mine := settingsPeerUID.Load()
	t.Cleanup(func() { settingsPeerUID.Store(mine) })
	settingsPeerUID.Store(mine + 1)

	requestSettingsPage(path)
	select {
	case <-asked:
		t.Fatal("a request from another user opened the settings page")
	case <-time.After(200 * time.Millisecond):
	}

	settingsPeerUID.Store(mine)
	requestSettingsPage(path)
	select {
	case <-asked:
	case <-time.After(2 * time.Second):
		t.Fatal("the request from this user did not reach the listener")
	}
}

// TestASecondProcessFindsTheLockHeld covers the lock across processes, which is
// what it is for: the copy that wins takes it, and the copy that starts beside it
// has to find the lock taken by another process and step aside. A test holding
// the lock itself would only be testing that a descriptor it took blocks a second
// descriptor in the same process.
func TestASecondProcessFindsTheLockHeld(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STATE_DIRECTORY", dir)
	t.Cleanup(func() {
		instanceTaken = false
		instanceLock = nil
		settingsListener = nil
	})

	holder := exec.Command(os.Args[0], "-test.run=TestTheCopyThatHoldsTheLockForAnotherProcess")
	holder.Env = append(os.Environ(), holdingLockIn+"="+dir)
	out, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var complaints strings.Builder
	holder.Stderr = &complaints
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Closing the pipe lets the copy end by itself; the kill is only there so
		// that a copy that is stuck cannot hold the test up.
		_ = stdin.Close()
		_ = holder.Process.Kill()
		_ = holder.Wait()
	}()

	line, err := bufio.NewReader(out).ReadString('\n')
	if strings.TrimSpace(line) != "holding" {
		t.Fatalf("the copy that was to hold the lock said %q: %v (%s)", line, err, complaints.String())
	}

	if AcquireSingleInstance(false) {
		t.Error("a copy started although another process was holding the lock")
	}
}

// holdingLockIn is the environment variable that turns the test below into the
// copy a test starts, and names the directory it holds the lock in.
const holdingLockIn = "SDAUTOLOGIN_TEST_HOLDING_LOCK_IN"

// TestTheCopyThatHoldsTheLockForAnotherProcess is not a test of its own: it is
// the copy that TestASecondProcessFindsTheLockHeld starts, which takes the lock
// and says so, so that the test can watch a second copy meet a lock held by
// another process.
func TestTheCopyThatHoldsTheLockForAnotherProcess(t *testing.T) {
	dir := os.Getenv(holdingLockIn)
	if dir == "" {
		t.Skip("the copy that a test starts, not a test of its own")
	}
	lock, err := openLockFile(filepath.Join(dir, lockFileName))
	if err != nil {
		t.Fatalf("the copy could not open the lock file: %v", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("the copy could not take the lock: %v", err)
	}
	fmt.Println("holding")
	// Hold it until the test that started us is done: the lock goes away with
	// the process, so it has to stay alive for as long as the test needs it.
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// TestWaitingWithNoSocketReturnsAtOnce covers the shape every platform has to
// have: a run that never published a socket has nothing to wait on, and the tray
// asks it to wait on every start.
func TestWaitingWithNoSocketReturnsAtOnce(t *testing.T) {
	saved := settingsListener
	settingsListener = nil
	t.Cleanup(func() { settingsListener = saved })

	done := make(chan struct{})
	go func() {
		WaitForSettingsRequest(func() {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("WaitForSettingsRequest blocked with no socket to listen on")
	}
}

// TestTheSharedTemporaryDirectoryIsNeverUsed covers the finding itself: the
// rendezvous names used to live in os.TempDir(), which every account on the
// machine can write to, so any of them could put a lock file there first and hold
// it. The candidate directories are asked here with a temporary directory of our
// own standing in for the shared one, and none of them may be it.
func TestTheSharedTemporaryDirectoryIsNeverUsed(t *testing.T) {
	shared := t.TempDir()
	if err := os.Chmod(shared, 0o1777); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	config := t.TempDir()
	t.Setenv("TMPDIR", shared)
	t.Setenv("STATE_DIRECTORY", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_CONFIG_HOME", config)

	if got := os.TempDir(); got != shared {
		t.Fatalf("os.TempDir() = %q, want the directory this test set up (%q)", got, shared)
	}
	got := instanceDirCandidates()
	if slices.Contains(got, shared) {
		t.Errorf("instanceDirCandidates() = %q, want the shared temporary directory left out", got)
	}
	if len(got) == 0 || got[0] != cache {
		t.Errorf("instanceDirCandidates() = %q, want %q first", got, cache)
	}
}
