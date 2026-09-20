//go:build !windows && !linux && !darwin

package main

// runUI runs as a background daemon on platforms without tray support
// (for example FreeBSD). The account is configured through the first run
// wizard or by editing config.json.
func runUI(svc *Service, cfg *Config, appDir string) {
	runHeadless(svc, cfg, appDir)
}
