package tray

import (
	"image/color"

	"sdautologin/internal/brand"
	"sdautologin/internal/service"
)

// iconAccent maps a session state to the accent color of the application mark:
// green online, grey offline, amber while signing in, red on an error, and a
// lighter grey before the first check has answered.
func iconAccent(st service.State) color.RGBA {
	switch st {
	case service.StateOnline:
		return brand.AccentOnline
	case service.StateOffline:
		return brand.AccentOffline
	case service.StateLoggingIn:
		return brand.AccentBusy
	case service.StateError:
		return brand.AccentError
	default:
		return brand.AccentUnknown
	}
}
