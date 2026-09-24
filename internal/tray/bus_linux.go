//go:build linux && !notray

package tray

import "github.com/godbus/dbus/v5"

// sessionBusError reports why no status notifier item can be put up, or nil when
// it can.
//
// It asks the one question fyne.io/systray asks before it exports anything, and
// asks it first because of what the toolkit does with the answer: a session bus
// it cannot reach makes its event loop wait for a quit that only the missing item
// could have asked for, so the program would run with no icon and nothing on
// screen to reach it with. A caller that knows this in advance can fall back to
// running without a tray.
func sessionBusError() error {
	_, err := dbus.SessionBus()
	return err
}
