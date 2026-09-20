package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const appTitle = "Campus Auto Login"

func main() {
	appDir := resolveAppDir()
	Log.Init(filepath.Join(appDir, "logs"))
	Log.Info("SDAutoLogin %s starting on %s, app dir %s", appVersion, platformName(), appDir)

	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		os.Exit(runCommand(args, appDir))
	}
	for _, a := range args {
		switch a {
		case "help", "-h", "--help", "-help":
			printUsage(os.Stdout)
			return
		case "version", "-v", "--version":
			fmt.Println(versionLine())
			return
		case "-test-login", "--test-login":
			os.Exit(cmdLogin(appDir))
		case "-cli", "--cli", "-daemon", "--daemon", "-headless":
			os.Exit(cmdRun(appDir))
		}
	}

	runGUI(appDir)
	Log.Info("SDAutoLogin stopped")
}

// runGUI is the default mode: a tray icon plus the settings page served on
// loopback for the browser.
func runGUI(appDir string) {
	// Starting the program a second time is a request to see the settings, not
	// a reason to add another tray icon.
	if !acquireSingleInstance(true) {
		return
	}
	cfg, err := LoadConfig(filepath.Join(appDir, "config.json"))
	if err != nil {
		Log.Error("loading config: %v", err)
	}
	SetLang(Lang(cfg.Language))
	Log.SetVerbose(cfg.VerboseLog)
	Log.Debug("config loaded from %s: %s", cfg.Path(), cfg.Summary())

	if !cfg.Configured() {
		Log.Warn("no account configured yet, opening the settings page")
	}
	runUI(newAppState(cfg), appDir)
}

func versionLine() string {
	return fmt.Sprintf("SDAutoLogin %s (%s/%s)", appVersion, platformName(), goArch())
}

// resolveAppDir prefers the directory holding the executable so config.json
// and logs/ stay next to it.
func resolveAppDir() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if !strings.Contains(dir, "go-build") && !strings.Contains(dir, "Temp") {
			return dir
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// launchCommand is the command line stored in the autostart entry.
func launchCommand() string {
	if exe, err := os.Executable(); err == nil {
		return `"` + exe + `"`
	}
	return ""
}

// runHeadless keeps checking in the background with no tray and no settings
// page. It is what the "run" command and the tray failure fallback use.
func runHeadless(state *appState, appDir string) {
	cfg := state.Config()
	if !cfg.Configured() {
		runSetupWizard(cfg)
	}
	var last State
	state.SetListener(func(s Snapshot) {
		if s.State != last {
			last = s.State
			Log.Info("status: %s", describeSnapshot(s))
		} else {
			Log.Debug("status: %s", describeSnapshot(s))
		}
	})
	state.Start()
	Log.Info("running in the background, config file %s, press Ctrl+C to quit", cfg.Path())

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	state.Stop()
	Log.Info("quit signal received")
}
