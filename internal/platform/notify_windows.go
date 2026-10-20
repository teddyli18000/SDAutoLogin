//go:build windows

package platform

// Notify has nothing to do on Windows. The tray shows a balloon tip through its
// own icon there, and that needs the icon handle this package does not hold.
func Notify(string, string) {}
