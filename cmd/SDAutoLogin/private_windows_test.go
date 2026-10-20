//go:build windows

package main

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// assertDirectoryIsPrivate reads the access list of dir back and checks that the
// account this program runs as is the only one on it. The mode bits say nothing
// here - os.Stat reports 0777 for every directory on NTFS - so the list is what
// there is to check, and reading it is the only way to tell a directory that was
// really restricted from one the program walked away from.
func assertDirectoryIsPrivate(t *testing.T, dir string) {
	t.Helper()

	descriptor, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the access list of %s: %v", dir, err)
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("%s has no access list to read back, which leaves it open to every account: %v", dir, err)
	}
	if acl == nil || acl.AceCount == 0 {
		t.Fatalf("%s carries an empty access list, which every account may reach through", dir)
	}
	if acl.AceCount != 1 {
		t.Errorf("%s names %d accounts, want the one this program runs as", dir, acl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatalf("reading the first entry of the access list of %s: %v", dir, err)
	}
	// The account an allowed entry names is the SID that follows the entry's
	// mask, which is where Windows puts it (see ACCESS_ALLOWED_ACE).
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("the account this program runs as: %v", err)
	}
	if !windows.EqualSid(sid, user.User.Sid) {
		t.Errorf("%s is reachable by an account other than the one this program runs as: %v", dir, sid)
	}
}
