package tray

// Options say how the tray serves the settings page and how it opens it.
type Options struct {
	// SettingsAddr is where to serve the settings page. An empty one keeps the on
	// demand loopback server, started when the page is asked for and shut down
	// once idle; a named address is served for as long as the program runs.
	SettingsAddr string
	// AppWindow opens the page in a browser window with none of the browser
	// chrome, instead of a tab in the default browser, so it looks like an
	// application of its own.
	AppWindow bool
}
