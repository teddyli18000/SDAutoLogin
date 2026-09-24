//go:build linux || darwin

package platform

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTheDirectoryProbeTellsWhatCanBeWritten covers the question the fallback
// rests on. The probe has to be right about the three shapes a machine can be in:
// a directory of its own, something that is not a directory, and a path that is not
// there, since true for the last two would leave the lock file where it cannot go.
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
