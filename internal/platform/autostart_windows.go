//go:build windows

package platform

import (
	"os/exec"
	"syscall"

	"sdautologin/internal/logging"
)

const (
	winRunKey   = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	winRunValue = "SDAutoLogin"
)

// AutostartEnabled reports whether the HKCU Run entry exists.
func AutostartEnabled() bool {
	cmd := exec.Command("reg", "query", winRunKey, "/v", winRunValue)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run() == nil
}

// SetAutostart adds or removes the HKCU Run entry, holding the command this run
// was started with (see SetAutostartArguments). reg runs hidden, otherwise a
// console window would flash on every toggle.
func SetAutostart(on bool) bool {
	command := autostartCommand()
	if on && command == "" {
		logging.Log.Warn("this program cannot name its own executable, so no autostart entry was written")
		return false
	}
	var cmd *exec.Cmd
	if on {
		cmd = exec.Command("reg", "add", winRunKey, "/v", winRunValue,
			"/t", "REG_SZ", "/d", command, "/f")
	} else {
		cmd = exec.Command("reg", "delete", winRunKey, "/v", winRunValue, "/f")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		// reg fails when the value is already absent, or was already there, so
		// the resulting state is what decides, not the exit code.
		if AutostartEnabled() == on {
			logging.Log.Debug("the autostart entry of %s already said what was asked of it", winRunKey)
			return true
		}
		logging.Log.Warn("updating autostart entry: %v", err)
		return false
	}
	if on {
		// The command line is what an entry that starts the wrong directory or
		// the wrong program is diagnosed from, and it never carries a password.
		logging.Log.Debug("autostart entry written: %s = %s", winRunKey, command)
	} else {
		logging.Log.Debug("autostart entry removed from %s", winRunKey)
	}
	return true
}
