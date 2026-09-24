//go:build !windows

package config

import (
	"fmt"
	"os"
)

// restrictToOwner has nothing to do where the mode bits are real: the file is
// written 0600, and the directory it lives in belongs to the user the program
// runs as.
func restrictToOwner(path string) error { return nil }

// protect puts the file back to being readable by its owner alone and reports
// what it had to put right.
//
// A mode can be the work of an older version of this program or of a reader who
// copied the file around, and the passwords in it are in clear text, so a run
// tightens it rather than only saying so. What it cannot tighten it reports.
func protect(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	mode := info.Mode().Perm()
	if mode&0o077 == 0 {
		return ""
	}
	if err := os.Chmod(path, configFileMode); err != nil {
		return fmt.Sprintf(
			"%s holds the passwords in clear text and is readable by users other than its owner (mode %04o), and it could not be tightened: %v",
			path, mode, err)
	}
	return fmt.Sprintf(
		"%s held the passwords in clear text and was readable by users other than its owner (mode %04o); it has been tightened to %04o",
		path, mode, configFileMode)
}
