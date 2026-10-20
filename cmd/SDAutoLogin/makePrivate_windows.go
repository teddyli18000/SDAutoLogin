//go:build windows

package main

import (
	"fmt"

	"sdautologin/internal/config"
)

// restrictToCaller gives dir an access list naming the account this program runs
// as and nobody else. Mode bits are no protection here: os.Chmod on Windows
// neither fails nor changes who can reach a directory, and os.Stat reports 0777
// for every directory on NTFS, so a check of the mode would pass on a directory
// every account on the machine can read and would fail on one that only its
// owner can. Nothing is read back, the same as for the configuration file and
// the password file: writing the list is the whole of what this platform can be
// asked, and a write that failed is reported as itself instead of being called
// something the file system would not do.
func restrictToCaller(dir string) error {
	if err := config.RestrictToOwner(dir); err != nil {
		return fmt.Errorf("%s could not be given an access list that keeps it to the account this program runs as: %w; pass --home=DIR to name a directory of your own", dir, err)
	}
	return nil
}
