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
// put it: without a home directory the path would be relative, landing the entry
// where the desktop never looks.
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

// AutostartEnabled reports whether the desktop entry file exists. A machine with
// no home directory to keep one in has none.
func AutostartEnabled() bool {
	path := autostartPath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// SetAutostart writes a desktop entry on Linux or a LaunchAgent on macOS, holding
// the command this run was started with (see SetAutostartArguments).
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
		"Exec=" + desktopCommand() + "\n" +
		"Terminal=false\n" +
		"X-GNOME-Autostart-enabled=true\n"
}

// desktopCommand is the value of the Exec key: this program by its full path and
// the arguments this run repeats. A desktop reads that value with rules of its
// own - inside a quoted argument a double quote, a backtick, a dollar sign and a
// backslash are escaped with a backslash, and a percent sign starts a field code
// wherever it stands - so the value is put together here rather than out of
// autostartCommand, which follows the rule a Windows command line is read by.
func desktopCommand() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return ""
	}
	out := desktopQuote(exe)
	for _, arg := range autostartArgumentList() {
		out += " " + desktopQuote(arg)
	}
	return out
}

// desktopQuote renders one argument of the Exec value. A percent sign is a field
// code wherever it stands, so a literal one is written as two - %i would otherwise
// turn into an icon option and %u into the address of the file - and an argument
// that a desktop would read as more than one word is quoted with its quotes,
// backticks, dollar signs and backslashes escaped. An argument that needs none of
// that is left as it is, since a person reads the entry too.
func desktopQuote(arg string) string {
	if arg == "" {
		return `""`
	}
	escaped := strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"`", "\\`",
		"$", "\\$",
		"%", "%%",
	).Replace(arg)
	if !strings.ContainsAny(escaped, " \t\n\"'\\><~|&;$*?#()`") {
		return escaped
	}
	return `"` + escaped + `"`
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
// would make the file invalid, leaving an entry that is there and does nothing.
func xmlEscape(s string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}
