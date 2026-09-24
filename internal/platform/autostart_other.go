//go:build !windows

package platform

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"sdautologin/internal/logging"
)

const appID = "sdautologin"

// autostartPath is the file the desktop reads, and "" when there is nowhere to
// put it: without a home directory the path would be relative, which lands the
// entry in whatever directory the program happened to start in, where the desktop
// never looks for it. An entry that cannot work is not written.
func autostartPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "LaunchAgents", "com."+appID+".plist")
	}
	return filepath.Join(home, ".config", "autostart", appID+".desktop")
}

// AutostartEnabled reports whether the desktop entry file exists, which is false
// on a machine with no home directory to keep one in.
func AutostartEnabled() bool {
	path := autostartPath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// SetAutostart writes a desktop entry on Linux or a LaunchAgent on macOS.
//
// What it writes is the command this run was started with, so an entry set up from
// a run given --home starts that directory's configuration rather than a fresh one
// beside the executable.
func SetAutostart(on bool) bool {
	path := autostartPath()
	if path == "" {
		logging.Log.Warn("this machine has no home directory to keep an autostart entry in")
		return false
	}
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			logging.Log.Warn("disabling autostart: %v", err)
			return false
		}
		logging.Log.Debug("autostart entry removed: %s", path)
		return true
	}
	if executablePath() == "" {
		logging.Log.Warn("this program cannot name its own executable, so no autostart entry was written")
		return false
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
	// The command line is what an entry that starts the wrong directory or the
	// wrong program is diagnosed from, and it never carries a password.
	logging.Log.Debug("autostart entry written: %s (exec %s)", path, autostartCommand())
	return true
}

func linuxDesktopEntry() string {
	return "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=SDAutoLogin\n" +
		"Comment=Campus network auto login\n" +
		"Exec=" + autostartCommand() + "\n" +
		"Terminal=false\n" +
		"X-GNOME-Autostart-enabled=true\n"
}

func macPlist() string {
	exe, _ := os.Executable()
	arguments := "<string>" + xmlEscape(exe) + "</string>"
	for _, arg := range autostartArgumentList() {
		arguments += "<string>" + xmlEscape(arg) + "</string>"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.` + appID + `</string>
  <key>ProgramArguments</key>
  <array>` + arguments + `</array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`
}

// xmlEscape makes a path safe to put in the property list. One holding & or <
// would make the file the desktop reads invalid, which leaves an entry that is
// there and does nothing.
func xmlEscape(s string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}
