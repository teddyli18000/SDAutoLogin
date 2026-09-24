//go:build darwin && !notray

package tray

// sessionBusError reports why no status notifier item can be put up, or nil when
// it can. macOS has no D-Bus: the tray there is the toolkit's own, built with
// cgo, and it either appears or the toolkit says so while it starts.
func sessionBusError() error { return nil }
