//go:build !windows

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// aclAttribute is where Linux keeps the access list of a file. The nine mode bits are
// the owner, group and others entries; an access list set on top of them is what makes
// a file readable by somebody the mode does not name.
const aclAttribute = "system.posix_acl_access"

// accessListState is what could be read about a file's access list of its own.
type accessListState int

const (
	// accessListNone: the file has no list beyond its mode bits.
	accessListNone accessListState = iota
	// accessListPresent: the file carries one, so who can read it cannot be told from
	// the nine mode bits.
	accessListPresent
	// accessListUnknown: the question could not be asked, which is not the same as an
	// answer of no.
	accessListUnknown
)

// RestrictToOwner has nothing to do where the mode bits are real: the file is
// created 0600 by the call that creates it, the directory it goes in is checked
// before anything is written into it, and the per-user directory the program
// keeps its settings in is put to 0700 by its caller on this platform. It is
// exported so that the one caller that is not built here, the directory, which
// only Windows has a use for, compiles against the same name.
func RestrictToOwner(path string) error { return nil }

// checkDirOwner refuses a directory another user can write to. A file written there
// can be replaced with one of theirs, and replacing a file is not something the mode
// of the file itself can prevent.
func checkDirOwner(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("the directory %q belongs to another user (uid %d), and a file written there could be replaced by that user", dir, st.Uid)
	}
	if mode := info.Mode().Perm(); mode&0o022 != 0 {
		return fmt.Errorf("the directory %q is writable by users other than its owner (mode %04o), and a file written there could be replaced by them", dir, mode)
	}
	return nil
}

// passwordFileAccess reports what the file the passwords come from lets other users do,
// and whether it may be read at all. Unlike config.json this file is not written by the
// program: it is where the deployment put it, so a mode that lets others read it is put
// right here and said out loud, and a file that belongs to somebody else is not read at
// all.
func passwordFileAccess(path string, info os.FileInfo) (notice string, read bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Sprintf("the password file %q belongs to another user (uid %d), so it is not read: nothing in it can be kept from that user", path, st.Uid), false
	}
	mode := info.Mode().Perm()
	if mode&0o077 == 0 {
		return "", true
	}
	if err := os.Chmod(path, configFileMode); err != nil {
		return fmt.Sprintf("the password file %q is readable by users other than its owner (mode %04o) and could not be tightened: %v; it is not read", path, mode, err), false
	}
	return fmt.Sprintf("the password file %q was readable by users other than its owner (mode %04o); it has been tightened to %04o", path, mode, configFileMode), true
}

// hasAccessList reports whether the file carries an access list of its own, which is
// what the nine mode bits do not show.
func hasAccessList(path string) accessListState {
	n, err := syscall.Getxattr(path, aclAttribute, nil)
	switch {
	case err == nil && n > 0:
		return accessListPresent
	case err == nil:
		return accessListNone
	case errors.Is(err, syscall.ENODATA), errors.Is(err, syscall.ENOTSUP):
		// No list, or a filesystem that cannot hold one: either way there is none.
		return accessListNone
	default:
		return accessListUnknown
	}
}

// protect puts the file back to being readable by its owner alone and reports what it
// had to put right and what it cannot confirm about it. A mode of 0600 is not proof on
// its own: an access list on the file and a directory others can write to both give
// access that the nine mode bits do not show, and a file that cannot be looked at is
// reported rather than passed over.
func protect(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		// Not being able to look is not the same as being all right.
		return fmt.Sprintf("the file that holds the passwords in clear text, %q, could not be checked: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Sprintf("the file that holds the passwords in clear text, %q, is a symbolic link: what it points at holds them, and this program writes the file itself and not through a link", path)
	}
	report := ""
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		if err := os.Chmod(path, configFileMode); err != nil {
			return fmt.Sprintf(
				"%q holds the passwords in clear text and is readable by users other than its owner (mode %04o), and it could not be tightened: %v",
				path, mode, err)
		}
		report = fmt.Sprintf(
			"%q held the passwords in clear text and was readable by users other than its owner (mode %04o); it has been tightened to %04o",
			path, mode, configFileMode)
	}
	switch hasAccessList(path) {
	case accessListPresent:
		report = joinNotes(report, fmt.Sprintf("%q carries an access list of its own beyond the nine mode bits, which this program does not write: who can read it cannot be told from the mode alone", path))
	case accessListUnknown:
		report = joinNotes(report, fmt.Sprintf("%q could not be asked whether it carries an access list of its own, so who can read it cannot be confirmed from here", path))
	}
	if dir := filepath.Dir(path); dir != "" {
		report = joinNotes(report, dirAccessReport(dir))
	}
	return report
}

// dirAccessReport says what the directory around the file allows, when that is more
// than the file's own mode suggests: a file another user can replace is a file whose
// contents are theirs to choose. A directory that others may only read is left out
// on purpose: it exposes the name and the size of the file, not what is in it, and
// a check that reports every 0755 home directory on every start is one that gets
// ignored. What the file itself lets others do is what the mode and the access list
// of the file are read for above.
func dirAccessReport(dir string) string {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Sprintf("the directory %q around the file that holds the passwords could not be checked: %v", dir, err)
	}
	mode := info.Mode().Perm()
	notes := make([]string, 0, 3)
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		notes = append(notes, fmt.Sprintf("it belongs to another user (uid %d)", st.Uid))
	}
	if mode&0o022 != 0 {
		notes = append(notes, fmt.Sprintf("users other than its owner may write in it (mode %04o), so the file can be replaced by one of them", mode))
	}
	if len(notes) == 0 {
		return ""
	}
	return fmt.Sprintf("the directory %q that holds the file is not its owner's alone: %s", dir, strings.Join(notes, ", and "))
}
