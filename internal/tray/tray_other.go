//go:build !windows && !linux && !darwin

package tray

import (
	"sdautologin/internal/app"
)

// Run has no tray on these platforms (for example FreeBSD), so it hands
// straight over to the background mode. The account is configured through the
// "config" command or by editing config.json.
func Run(_ *app.AppState, fallback func()) {
	fallback()
}
