package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"sdautologin/internal/app"
	"sdautologin/internal/config"
	"sdautologin/internal/i18n"
	"sdautologin/internal/logging"
	"sdautologin/internal/platform"
	"sdautologin/internal/service"
	"sdautologin/internal/tray"

	// The Windows icon, version information and application manifest live in
	// that package as a linked object file, so it has to be part of the build.
	_ "sdautologin/winres"
)

func main() {
	appDir := resolveAppDir()
	args := os.Args[1:]
	// The log is opened before the console is attached, so a console that
	// cannot be attached is recorded instead of being lost.
	logging.Log.Init(filepath.Join(appDir, "logs"))
	if len(args) > 0 {
		// Arguments mean a command was asked for, so a terminal is waiting for
		// the answer. See attachConsole.
		attachConsole()
	}
	logging.Log.Info("SDAutoLogin %s starting on %s, app dir %s", app.Version, app.PlatformName(), appDir)

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
	logging.Log.Info("SDAutoLogin stopped")
}

// runGUI is the default mode: a tray icon plus the settings page served on
// loopback for the browser.
func runGUI(appDir string) {
	// Starting the program a second time is a request to see the settings, not
	// a reason to add another tray icon.
	if !platform.AcquireSingleInstance(true) {
		return
	}
	cfg, err := config.LoadConfig(filepath.Join(appDir, "config.json"))
	if err != nil {
		logging.Log.Error("loading config: %v", err)
	}
	i18n.SetLang(i18n.Lang(cfg.Language))
	logging.Log.SetVerbose(cfg.VerboseLog)
	logging.Log.Debug("config loaded from %s: %s", cfg.Path(), cfg.Summary())

	state := app.New(cfg)
	if !cfg.Configured() {
		logging.Log.Warn("no account configured yet, opening the settings page")
	}
	tray.Run(state, func() { runHeadless(state, appDir) })
}

func versionLine() string {
	return fmt.Sprintf("SDAutoLogin %s (%s/%s)", app.Version, app.PlatformName(), app.Arch())
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

// runHeadless keeps checking in the background with no tray and no settings
// page. It is what the "run" command and the tray failure fallback use.
func runHeadless(state *app.AppState, appDir string) {
	cfg := state.Config()
	if !cfg.Configured() {
		runSetupWizard(cfg)
	}
	var last service.State
	state.SetListener(func(s service.Snapshot) {
		if s.State != last {
			last = s.State
			logging.Log.Info("status: %s", service.DescribeSnapshot(s))
		} else {
			logging.Log.Debug("status: %s", service.DescribeSnapshot(s))
		}
	})
	state.Start()
	logging.Log.Info("running in the background, config file %s, press Ctrl+C to quit", cfg.Path())

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	state.Stop()
	logging.Log.Info("quit signal received")
}
