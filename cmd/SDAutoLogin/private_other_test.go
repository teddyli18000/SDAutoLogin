//go:build !windows

package main

import (
	"os"
	"testing"
)

// assertDirectoryIsPrivate checks that dir is reachable by its owner alone. On
// these platforms that is the mode bits: 0700 is the whole story, and a
// directory another user can reach is what the check is for.
func assertDirectoryIsPrivate(t *testing.T, dir string) {
	t.Helper()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("%s is mode %#o, want 0700", dir, mode)
	}
}
