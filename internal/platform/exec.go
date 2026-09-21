package platform

import (
	"os"
)

// executablePath is the command that starts this program, quoted so it survives
// a path with spaces. It is what the autostart entry stores.
func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return `"` + exe + `"`
}
