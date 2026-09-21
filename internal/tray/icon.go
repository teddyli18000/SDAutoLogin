package tray

import (
	"image/color"

	"sdautologin/internal/brand"
	"sdautologin/internal/service"
)

// iconAccent maps a session state to the accent color of the application mark.
func iconAccent(st service.State) color.RGBA {
	switch st {
	case service.StateOnline:
		return brand.AccentOnline
	case service.StateLoggingIn, service.StateOffline:
		return brand.AccentBusy
	case service.StateError:
		return brand.AccentError
	default:
		return brand.AccentOffline
	}
}
