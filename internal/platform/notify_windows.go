//go:build windows

package platform

// Notify has nothing to do on Windows: the tray shows a balloon tip through its
// own icon there, which is the notification that desktop expects and the one
// that needs the icon handle this package does not hold.
func Notify(string, string) {}
