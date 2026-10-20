//go:build !windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"sdautologin/internal/logging"
)

// OpenExternal hands a URL or a file to the desktop's default handler. The
// handler is looked for in the place the system installs it in before its bare
// name is looked for on PATH, so a directory someone put earlier on PATH does not
// get to decide which program opens the address.
func OpenExternal(target string) error {
	cmd, path := openCommand(target)
	if cmd == nil {
		return fmt.Errorf("no program that could open %s was found", target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	logging.Log.Debug("opening %s through %s", target, path)
	// Reaped on its own goroutine: the caller does not wait for the handler, and
	// a run that lasts for months would keep one zombie per click otherwise.
	go func() { _ = cmd.Wait() }()
	return nil
}

// openCommand is the program the desktop's handler is asked through: the
// launcher on macOS, a program the user named in $BROWSER when there is one, and
// the desktop's own handler otherwise.
func openCommand(target string) (*exec.Cmd, string) {
	if runtime.GOOS == "darwin" {
		return externalCommand(helperNames("open"), target)
	}
	if browser := os.Getenv("BROWSER"); browser != "" {
		if cmd, path := browserCommand(browser, target); cmd != nil {
			return cmd, path
		}
		// A $BROWSER this program cannot run falls through to the desktop's own
		// handler rather than leaving the address unopened, which is what the
		// old code did when the value held an option or was not a program.
	}
	return externalCommand(helperNames("xdg-open"), target)
}
