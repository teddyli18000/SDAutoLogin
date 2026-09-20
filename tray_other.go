//go:build !windows && !linux && !darwin

package main

// runUI runs as a background daemon on platforms without tray support
// (for example FreeBSD). The account is configured through the "config"
// command or by editing config.json.
func runUI(state *appState, appDir string) {
	runHeadless(state, appDir)
}
