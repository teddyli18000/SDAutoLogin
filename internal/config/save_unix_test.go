//go:build !windows

package config

import (
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestASaveThatFailsPartWayThroughLeavesTheOldFileAlone covers the write that fails
// after it has started: the file that is there is never opened, let alone truncated,
// so the configuration written last survives a disk that filled up or a quota that ran
// out. The write limit is what makes such a write fail at a known point instead of at
// a random one, and it is put back before anything is read or logged.
func TestASaveThatFailsPartWayThroughLeavesTheOldFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", Password: "keep-me", ISPs: []string{""}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) < 256 {
		t.Fatalf("the configuration to lose is only %d bytes", len(before))
	}

	// A limit far below the size of the configuration, so the copy that is written
	// first cannot be finished and nothing may be put in place of the file.
	signal.Ignore(syscall.SIGXFSZ)
	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Skipf("cannot read the write limit: %v", err)
	}
	limit := uint64(64)
	if old.Cur != 0 && old.Cur < limit {
		limit = old.Cur
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: limit, Max: old.Max}); err != nil {
		t.Skipf("cannot set the write limit: %v", err)
	}

	cfg.Accounts[0].Password = "a longer secret that does not fit under the limit"
	saveErr := cfg.Save()
	if restoreErr := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old); restoreErr != nil {
		t.Fatalf("restoring the write limit: %v", restoreErr)
	}
	if saveErr == nil {
		t.Skip("the write limit was not enforced, so there is no failed write to check")
	}
	if !errors.Is(saveErr, syscall.EFBIG) {
		t.Logf("the write failed with %v, which is still a failure to check", saveErr)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the configuration that was there is gone: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("a write that failed part way through changed the file:\n--- before\n%s\n--- after\n%s", before, after)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("a write that failed left %s behind", e.Name())
		}
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("the configuration that survived the failed write does not load: %v", err)
	}
	if len(reloaded.Accounts) != 1 || reloaded.Accounts[0].Password != "keep-me" {
		t.Errorf("the surviving configuration is not the one that was written: %+v", reloaded.Accounts)
	}
}

// TestAnAccessListOfItsOwnIsReported covers the file that is described by more than its
// nine mode bits. A list that names a user cannot be read off the mode, so a check that
// says nothing about one is a check that cannot be trusted. The list is written by hand
// here, since setfacl is not part of every system this runs on: the mask entry is what
// the group bits of the mode are taken from, so a mask of none leaves the mode reading
// 0600 while the entries are the part that does not.
func TestAnAccessListOfItsOwnIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	acl := []byte{
		0x02, 0x00, 0x00, 0x00, // version 2
		0x01, 0x00, 0x06, 0x00, 0xff, 0xff, 0xff, 0xff, // owner: read and write
		0x02, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, // the user root: read
		0x04, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff, // the group: nothing
		0x10, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff, // the mask: nothing
		0x20, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff, // others: nothing
	}
	if err := syscall.Setxattr(path, aclAttribute, acl, 0); err != nil {
		t.Skipf("this filesystem does not take an access list: %v", err)
	}
	if got := fileMode(t, path); got != 0o600 {
		t.Fatalf("the mode reads %04o, and this test needs a file whose mode alone says it is the owner's", got)
	}

	report := cfg.Protect()
	if !strings.Contains(report, "access list") {
		t.Errorf("a file carrying an access list of its own was reported as %q", report)
	}
}

// TestTheDirectoryAroundTheConfigFileIsReportedWhenOthersCanWriteIt covers the part of
// the startup check that the mode of the file cannot see: a file another user can
// replace is a file whose contents are theirs to choose, and no mode of the file itself
// prevents the replacement. The report names the directory and what it allows, and the
// file is left as it is, since it is the directory that is wrong.
func TestTheDirectoryAroundTheConfigFileIsReportedWhenOthersCanWriteIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}

	report := cfg.Protect()
	if report == "" {
		t.Fatal("a configuration file in a directory others can write in was not reported")
	}
	if !strings.Contains(report, "0777") {
		t.Errorf("the report does not name the mode it found: %q", report)
	}
	if !strings.Contains(report, dir) {
		t.Errorf("the report does not name the directory that is wrong: %q", report)
	}
	if got := fileMode(t, path); got != 0o600 {
		t.Errorf("the file itself is %04o after Protect, want 0600", got)
	}
}

// TestAConfigFileThatCannotBeLookedAtOrIsALinkIsReported covers the two shapes the check
// has to say something about without touching them. Not being able to look at the file
// is not the same as finding it all right, and a file that is a link is written through
// by whoever put the link there rather than by this program, so neither is passed over
// in silence.
func TestAConfigFileThatCannotBeLookedAtOrIsALinkIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if report := cfg.Protect(); !strings.Contains(report, "could not be checked") {
		t.Errorf("a configuration file that is not there is reported as %q, want it said that it could not be checked", report)
	}

	target := filepath.Join(dir, "elsewhere.txt")
	if err := os.WriteFile(target, []byte("not the settings\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	if report := cfg.Protect(); !strings.Contains(report, "symbolic link") {
		t.Errorf("a configuration file that is a symbolic link is reported as %q, want it said that it is one", report)
	}
}
