//go:build !windows && !linux && !darwin

package main

// acquireSingleInstance is not implemented on these platforms, starting twice
// is simply allowed.
func acquireSingleInstance(_ bool) bool { return true }
