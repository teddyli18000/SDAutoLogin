//go:build !windows

package main

import (
	"fmt"
	"os"
)

// restrictToCaller puts dir to 0700 and reads the mode back. The read-back is
// half the point: a chmod that returns no error has not necessarily changed
// anything on a filesystem that will not keep the mode, and a directory another
// user can reach is not a place for a configuration that may hold a password.
// Chmod rather than MkdirAll, because the directory may already be there.
func restrictToCaller(dir string) error {
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("%s cannot be made private: %w; pass --home=DIR to name a directory of your own", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s cannot be read back: %w", dir, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%s is mode %#o, which other users can reach and which this file system will not change; pass --home=DIR to name a directory of your own", dir, mode)
	}
	return nil
}
