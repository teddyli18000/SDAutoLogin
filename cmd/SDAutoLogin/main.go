package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"sdautologin/internal/app"
	"sdautologin/internal/logging"
	"sdautologin/internal/platform"
	"sdautologin/internal/service"
	"sdautologin/internal/tray"

	// The Windows icon, version information and application manifest live in
	// that package as a linked object file, so it has to be part of the build.
	_ "sdautologin/winres"
)

func main() {
	args := os.Args[1:]
	// The options are read first, before anything is opened or printed: --home
	// decides where config.json and logs/ live, and a failure still has to be
	// reported on a console that may not exist yet, see attachConsole.
	opts, rest, err := parseOptions(args)
	appDir := resolveAppDir(opts.home)
	// The log is opened before the console is attached, so a console that
	// cannot be attached is recorded instead of being lost.
	logging.Log.Init(filepath.Join(appDir, "logs"))
	if len(args) > 0 {
		// Arguments mean a command was asked for, so a terminal is waiting for
		// the answer. See attachConsole.
		attachConsole()
	}
	logging.Log.Info("SDAutoLogin %s starting on %s, app dir %s", app.Version, app.PlatformName(), appDir)

	if err != nil {
		// The reason also goes to the log: the Windows build is linked as a GUI
		// program, so a start that fails on an argument leaves nothing on screen
		// to look at, and app.log is then the only trace of it.
		logging.Log.Error("%v", err)
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printUsage(os.Stderr)
		os.Exit(2)
	}

	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		os.Exit(runCommand(rest, appDir, opts))
	}
	// The shortcuts take the same account options the commands do, which is what
	// lets a script configure a machine without a terminal.
	for _, a := range rest {
		switch a {
		case "help", "-h", "--help", "-help":
			printUsage(os.Stdout)
			return
		case "version", "-v", "--version":
			fmt.Println(versionLine())
			return
		case "-test-login", "--test-login":
			os.Exit(cmdLogin(appDir, opts))
		case "-cli", "--cli", "-daemon", "--daemon", "-headless":
			os.Exit(cmdRun(appDir, opts))
		}
	}

	runGUI(appDir, opts)
	logging.Log.Info("SDAutoLogin stopped")
}

// runGUI is the default mode: a tray icon plus the settings page served on
// loopback for the browser.
func runGUI(appDir string, opts options) {
	// Starting the program a second time is a request to see the settings, not
	// a reason to add another tray icon.
	if !platform.AcquireSingleInstance(true) {
		return
	}
	cfg, ok := loadForCLI(appDir, opts)
	if !ok {
		// The settings page is exactly how a broken configuration is fixed, so
		// the program starts on the defaults instead of refusing to run.
		logging.Log.Warn("starting on the defaults, the settings page can correct the configuration")
	}
	logging.Log.Debug("config loaded from %s: %s", cfg.Path(), cfg.Summary())

	state := app.New(cfg)
	if !cfg.Configured() {
		logging.Log.Warn("no account configured yet, opening the settings page")
	}
	// --app turns the settings page into a window of its own. Without it the
	// page stays what it has always been: a tab in the default browser, opened
	// when the tray is clicked.
	trayOpts := tray.Options{AppWindow: opts.app != nil}
	if opts.app != nil {
		trayOpts.SettingsAddr = *opts.app
	}
	tray.Run(state, trayOpts, func() { runHeadless(state, nil) })
}

func versionLine() string {
	return fmt.Sprintf("SDAutoLogin %s (%s/%s)", app.Version, app.PlatformName(), app.Arch())
}

// resolveAppDir picks the directory that holds config.json and logs/. An
// explicit --home wins, otherwise the directory holding the executable is
// preferred so a portable copy keeps its settings next to itself.
func resolveAppDir(home *string) string {
	if home != nil && *home != "" {
		return *home
	}
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

// runHeadless keeps checking in the background with no tray. stop, when not nil,
// ends it early, which is how the settings page quits a background run.
func runHeadless(state *app.AppState, stop <-chan struct{}) {
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
	select {
	case <-ch:
		logging.Log.Info("quit signal received")
	case <-stop:
		logging.Log.Info("quit requested from the settings page")
	}
	state.Stop()
}
