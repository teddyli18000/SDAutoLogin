//go:build !windows && !linux && !darwin

package platform

// AcquireSingleInstance is not implemented on these platforms, starting twice
// is simply allowed.
func AcquireSingleInstance(_ bool) bool { return true }

// WaitForSettingsRequest has nothing to listen to here.
func WaitForSettingsRequest(_ func()) {}
