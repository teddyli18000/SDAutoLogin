//go:build (!windows && !linux && !darwin) || notray

package tray

import (
	"sdautologin/internal/app"
)

// HasTray reports whether this build puts up a tray icon. These builds have none,
// so an option that needs one is refused instead of being ignored.
const HasTray = false

// Run has no tray on these platforms (for example FreeBSD, or a Windows or Linux
// build made with the notray tag), so it hands over to the background mode with
// no settings server of its own. The account is configured with the "config"
// command.
func Run(_ *app.AppState, _ Options, fallback func(*app.Server)) {
	fallback(nil)
}
