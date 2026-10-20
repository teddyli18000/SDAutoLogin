//go:build linux && !notray

package tray

import "github.com/godbus/dbus/v5"

// sessionBusError reports why no status notifier item can be put up, or nil when
// it can. It asks the question fyne.io/systray asks before it exports anything:
// a session bus the toolkit cannot reach leaves it with no icon and no way in.
func sessionBusError() error {
	_, err := dbus.SessionBus()
	return err
}
