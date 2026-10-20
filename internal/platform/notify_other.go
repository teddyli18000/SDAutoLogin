//go:build !windows

package platform

import (
	"runtime"

	"sdautologin/internal/logging"
)

// Notify shows a desktop notification with the given title and body. It is best
// effort: the tray toolkit has none of its own, so the helper the desktop
// already has is used, and a missing one is not a failure, only a debug line.
func Notify(title, body string) {
	name, args := notifyCommand(runtime.GOOS, title, body)
	if name == "" {
		return
	}
	cmd, path := externalCommand(helperNames(name), args...)
	if cmd == nil {
		logging.Log.Debug("desktop notification through %s failed: no such program", name)
		return
	}
	if err := cmd.Start(); err != nil {
		logging.Log.Debug("desktop notification through %s failed: %v", path, err)
		return
	}
	go func() { _ = cmd.Wait() }()
}

// helperNames is where a helper program is looked for: the two places a system
// installs one in first, and its bare name as the fallback for a system that puts
// it somewhere else. A helper found by its bare name is one a directory earlier
// on PATH could name instead, which is why the names come last.
func helperNames(name string) []string {
	return []string{"/usr/bin/" + name, "/bin/" + name, name}
}
