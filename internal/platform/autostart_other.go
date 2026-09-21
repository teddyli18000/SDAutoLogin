//go:build !windows

package platform

import (
	"os"
	"path/filepath"
	"runtime"

	"sdautologin/internal/logging"
)

const appID = "sdautologin"

func autostartPath() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "LaunchAgents", "com."+appID+".plist")
	}
	return filepath.Join(home, ".config", "autostart", appID+".desktop")
}

// AutostartEnabled reports whether the desktop entry file exists.
func AutostartEnabled() bool {
	_, err := os.Stat(autostartPath())
	return err == nil
}

// SetAutostart writes a desktop entry on Linux or a LaunchAgent on macOS.
func SetAutostart(on bool) bool {
	path := autostartPath()
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			logging.Log.Warn("disabling autostart: %v", err)
			return false
		}
		return true
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logging.Log.Warn("creating autostart directory: %v", err)
		return false
	}
	content := linuxDesktopEntry()
	if runtime.GOOS == "darwin" {
		content = macPlist()
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		logging.Log.Warn("writing autostart entry: %v", err)
		return false
	}
	return true
}

func linuxDesktopEntry() string {
	return "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=SDAutoLogin\n" +
		"Comment=Campus network auto login\n" +
		"Exec=" + executablePath() + "\n" +
		"Terminal=false\n" +
		"X-GNOME-Autostart-enabled=true\n"
}

func macPlist() string {
	exe, _ := os.Executable()
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.` + appID + `</string>
  <key>ProgramArguments</key>
  <array><string>` + exe + `</string></array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`
}
