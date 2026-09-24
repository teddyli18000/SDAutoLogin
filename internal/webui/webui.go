// Package webui holds the settings page that is compiled into the executable.
// The app package serves it over loopback, reading it through Assets.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed assets
var assets embed.FS

// Assets returns the settings page files, rooted at the directory that holds
// them, so callers read "index.html" and not "assets/index.html".
func Assets() fs.FS {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		// Impossible: the directory is embedded right above. Returning the
		// unrooted tree keeps the program serving something instead of panicking.
		return assets
	}
	return sub
}
