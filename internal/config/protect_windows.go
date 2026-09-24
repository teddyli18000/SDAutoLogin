//go:build windows

package config

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// restrictToOwner replaces the file's access list with one entry, for the user
// the program is running as, and stops the list from being inherited from the
// directory around it.
//
// The mode bits a Unix write would use mean nothing here: Go can set the
// read-only attribute on Windows and nothing else, so a file written with 0600
// is readable by everyone who can read the directory it sits in — which, for an
// installation under Program Files, is every user of the machine. The access
// list is the only thing that keeps the passwords in the file to the account
// they belong to.
func restrictToOwner(path string) error {
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
//
// There is nothing to ask a file on Windows about its access without reading the
// list back, so this writes the list rather than comparing it: the write is the
// same one either way, and it is the only thing that keeps a file out of reach of
// the other accounts on the machine.
func protect(path string) string {
	if err := restrictToOwner(path); err != nil {
		return fmt.Sprintf(
			"the file that holds the passwords in clear text, %s, could not be given an access list of its own: %v",
			path, err)
	}
	return ""
}
