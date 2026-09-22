package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"sdautologin/internal/logging"
)

// OpenAppWindow opens target in a browser window that carries none of the
// browser chrome, so the settings page looks like an application of its own
// rather than a tab among everything else the user has open.
//
// Only the Chromium family can do that, and which member of it is installed is
// not something this program can know, so the ones that are usually there are
// tried in turn. When none of them is found, or the one that was found refuses
// to start, the page goes to the default browser instead: a tab is worse than a
// window of its own, but better than a program that appears to do nothing.
func OpenAppWindow(target string) error {
	browser := findAppBrowser(
		appBrowserCandidates(runtime.GOOS, os.Getenv),
		isFile,
		exec.LookPath,
	)
	if browser == "" {
		logging.Log.Info("no browser that can open a window of its own was found, opening the settings page in the default browser")
		return OpenExternal(target)
	}

	name, args := appWindowCommand(runtime.GOOS, browser, target)
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		logging.Log.Warn("starting %s: %v, opening the settings page in the default browser", browser, err)
		return OpenExternal(target)
	}
	// The browser outlives this program by design, but something still has to
	// collect it when it ends: without this it stays a zombie for as long as the
	// tray runs, which is a long time and can happen more than once.
	go func() { _ = cmd.Wait() }()
	logging.Log.Info("settings page opened in %s, in a window of its own", browser)
	return nil
}

// appWindowCommand is how a browser is asked for a window of its own. macOS
// cannot start a browser bundle with that flag directly, so the launcher is
// asked to start the bundle and to pass the flag on to it.
func appWindowCommand(goos, browser, target string) (string, []string) {
	if goos == "darwin" {
		return "open", []string{"-na", browser, "--args", "--app=" + target}
	}
	return browser, []string{"--app=" + target}
}

// appBrowserCandidates lists the browsers that can open a window of their own,
// the ones to try first coming first. A candidate is either an absolute path,
// on the systems that install into a known place, or a bare name to be looked
// up on PATH.
func appBrowserCandidates(goos string, getenv func(string) string) []string {
	switch goos {
	case "windows":
		var bases []string
		for _, name := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if base := getenv(name); base != "" {
				bases = append(bases, base)
			}
		}
		// Chrome first: it is the one people install on purpose. Edge comes
		// next because it is already on every Windows that has not been stripped
		// of it, so it is what most machines will end up using.
		var out []string
		for _, rel := range []string{
			filepath.Join("Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join("Microsoft", "Edge", "Application", "msedge.exe"),
		} {
			for _, base := range bases {
				out = append(out, filepath.Join(base, rel))
			}
		}
		return out
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app",
			"/Applications/Microsoft Edge.app",
			"/Applications/Chromium.app",
			"/Applications/Brave Browser.app",
		}
	default:
		// The names the distributions put on PATH, in the order they are worth
		// trying. A name that is missing is skipped.
		return []string{
			"google-chrome",
			"google-chrome-stable",
			"chromium",
			"chromium-browser",
			"microsoft-edge",
			"microsoft-edge-stable",
			"brave-browser",
		}
	}
}

// findAppBrowser returns the first candidate that is there: an absolute path is
// looked for on the file system, anything else on PATH. An empty result means
// none of them is installed.
func findAppBrowser(candidates []string, isFile func(string) bool, lookPath func(string) (string, error)) string {
	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) {
			if isFile(candidate) {
				return candidate
			}
			continue
		}
		if found, err := lookPath(candidate); err == nil {
			return found
		}
	}
	return ""
}

// isFile reports whether path names a file that is there.
func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
