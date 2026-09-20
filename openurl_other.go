//go:build !windows

package main

import (
	"os"
	"os/exec"
	"runtime"
)

// openExternal hands a URL or a file to the desktop's default handler.
func openExternal(target string) error {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", target)
	case os.Getenv("BROWSER") != "":
		cmd = exec.Command(os.Getenv("BROWSER"), target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
