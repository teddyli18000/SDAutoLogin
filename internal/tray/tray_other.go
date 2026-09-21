//go:build (!windows && !linux && !darwin) || (notray && !windows)

package tray

import (
	"sdautologin/internal/app"
)

// HasTray reports whether this build puts up a tray icon at all. These builds
// have none, so an option that needs one is refused instead of being ignored.
const HasTray = false

// Run has no tray on these platforms (for example FreeBSD, or a Linux build made
// with the notray tag), so it hands straight over to the background mode. The
// account is configured through the "config" command, on the command line, or
// through the settings page of a background run.
func Run(_ *app.AppState, _ Options, fallback func()) {
	fallback()
}
