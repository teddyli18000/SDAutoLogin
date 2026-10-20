//go:build windows

package config

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// RestrictToOwner replaces the access list of a file or a directory with one
// entry for the user the program runs as, and stops the list being inherited
// from the directory around it. Mode bits do nothing on Windows: a file written
// 0600 is readable by anyone who can read its directory, and a directory made
// 0700 is reachable by anyone who can open it; the list is the only protection
// left. It is exported because the per-user directory the program keeps its
// settings in (cmd/SDAutoLogin) has to be kept to its account the same way, and
// this platform has no other way to say it.
func RestrictToOwner(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("the user this program runs as: %w", err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("building the access list: %w", err)
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil)
}

// protect sets the file's access list again and reports what it could not do.
// Nothing can be asked about a file's access on Windows without reading the
// list back, so this writes the list: the write is the only way to keep the
// file out of reach of the other accounts on the machine.
func protect(path string) string {
	if err := RestrictToOwner(path); err != nil {
		return fmt.Sprintf(
			"the file that holds the passwords in clear text, %s, could not be given an access list of its own: %v",
			path, err)
	}
	return ""
}

// passwordFileAccess gives the file the passwords are read from an access list of its
// own, the way the configuration file is given one, and reports whether it may be read
// at all. There is no user id to compare here and the list of a file cannot be read
// back with what this program uses, so the list is written instead: one that could not
// be set is one the file is not read through, since what is in it is in clear text.
func passwordFileAccess(path string, info os.FileInfo) (notice string, read bool) {
	if err := RestrictToOwner(path); err != nil {
		return fmt.Sprintf("the password file %q could not be given an access list of its own, so it is not read: %v", path, err), false
	}
	return "", true
}

// checkDirOwner has no user id to compare on Windows: what decides is the access list
// of the directory, and the file is given a list of its own before the passwords go
// into it. What such a list leaves out is reported by protect.
func checkDirOwner(dir string) error { return nil }
