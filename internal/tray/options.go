package tray

// Options say how the tray serves the settings page and how it opens it.
type Options struct {
	// SettingsAddr is where to serve the settings page. An empty one keeps the
	// on demand loopback server, started when the page is asked for and shut
	// down again once it has been idle. A named address is served from the start
	// and stays up for as long as the program runs, for a machine without a tray.
	SettingsAddr string
	// AppWindow opens the page in a browser window with none of the browser
	// chrome, instead of a tab in the default browser, so it looks like an
	// application of its own.
	AppWindow bool
}
