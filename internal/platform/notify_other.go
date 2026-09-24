//go:build !windows

package platform

import (
	"os/exec"
	"runtime"

	"sdautologin/internal/logging"
)

// Notify shows a desktop notification with the given title and body. It is best
// effort: the tray toolkit has no notification of its own, so the helper each
// desktop already has is used, and a machine with no session, no notification
// daemon, or no helper installed is not a failure. The only trace is a debug line.
func Notify(title, body string) {
	name, args := notifyCommand(runtime.GOOS, title, body)
	if name == "" {
		return
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		logging.Log.Debug("desktop notification through %s failed: %v", name, err)
		return
	}
	// The helper is short lived, but it still has to be collected when it ends.
	go func() { _ = cmd.Wait() }()
}
