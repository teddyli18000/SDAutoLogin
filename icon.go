package main

import (
	"image/color"

	"sdautologin/internal/brand"
)

// iconAccent maps a session state to the accent color of the application mark.
func iconAccent(st State) color.RGBA {
	switch st {
	case StateOnline:
		return brand.AccentOnline
	case StateLoggingIn, StateOffline:
		return brand.AccentBusy
	case StateError:
		return brand.AccentError
	default:
		return brand.AccentOffline
	}
}
