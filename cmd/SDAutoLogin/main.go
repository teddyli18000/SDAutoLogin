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
	// An autostart entry is written later, by the tray or by the settings page, and
	// it has to start the program this run is. The option that decides where the
	// settings live is recorded here; nothing else is, since the rest of the
	// command line is for this run only, or a password.
	platform.SetAutostartArguments(autostartArguments(opts))
	// The log is opened before the console is attached, so a console that
	// cannot be attached is recorded instead of being lost. --no-log is known
	// here, before the configuration is read, so a run that asks for no log file
	// does not create one.
	logging.Log.Init(filepath.Join(appDir, "logs"), !opts.noLog)
	if len(args) > 0 {
		// Arguments mean a command was asked for, so a terminal is waiting for
		// the answer. See attachConsole.
		attachConsole()
	}
	logging.Log.Info("SDAutoLogin %s starting on %s, app dir %s", app.FullVersion(), app.PlatformName(), appDir)

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
	// The shortcuts take the same account options the commands do, so a script can
	// configure a machine without a terminal.
	for _, a := range rest {
		switch a {
		case "help", "-h", "--help", "-help":
			printUsage(os.Stdout)
			return
		case "version", "-v", "--version":
			fmt.Println(versionLine())
			return
		case "-test-login", "--test-login":
			exitIfBadOptions(opts, "login", cmdLogin, appDir)
		case "-cli", "--cli", "-daemon", "--daemon", "-headless":
			exitIfBadOptions(opts, "run", cmdRun, appDir)
		}
	}

	// The tray is the mode that names no command, so an option belonging to one
	// cannot be honoured here. Refusing it says what is wrong; dropping it would
	// leave a script waiting for a report that never comes.
	if err := opts.checkTrayOptions(); err != nil {
		logging.Log.Error("%v", err)
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printUsage(os.Stderr)
		os.Exit(2)
	}
	// Only the tray mode has a window to open, and a build without a tray says so
	// rather than starting in silence.
	if err := checkTray(opts, tray.HasTray); err != nil {
		logging.Log.Error("%v", err)
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printUsage(os.Stderr)
		os.Exit(2)
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
		// A broken configuration can be fixed from the settings page, so the
		// program starts on the defaults instead of refusing to run.
		logging.Log.Warn("starting on the defaults, the settings page can correct the configuration")
	}
	logging.Log.Debug("config loaded from %s: %s", cfg.Path(), cfg.Summary())

	state := app.New(cfg)
	if !cfg.Configured() {
		// Said here rather than by the tray, since both the tray and the fallback
		// open the page for this reason, and the two of them logging it would be
		// two lines about one thing.
		logging.Log.Warn("no account configured yet, opening the settings page")
	}
	// --app turns the settings page into a window of its own. Without it the page
	// stays a tab in the default browser, opened when the tray is clicked.
	trayOpts := tray.Options{AppWindow: opts.app != nil}
	if opts.app != nil {
		trayOpts.SettingsAddr = *opts.app
	}
	tray.Run(state, trayOpts, func(server *app.Server) { runFallback(state, opts, server) })
}

// runFallback keeps checking with no tray and no window, which is what happens on
// a machine where the icon cannot be put up. The page is the only way into such a
// machine, so it is served here: on the server the tray had already built, or on
// one made here when there was no tray to build one. Either way its exit button
// ends this run, rather than posting a message to a window that never got a
// message loop.
func runFallback(state *app.AppState, opts options, existing *app.Server) {
	stop := make(chan struct{})

	server := existing
	if server == nil {
		addr := ""
		if opts.app != nil {
			addr = *opts.app
		}
		server = app.NewServer(state, addr, func() { close(stop) })
		defer server.Stop()
	} else {
		server.SetOnQuit(func() { close(stop) })
	}

	url, err := server.URL()
	if err != nil {
		logging.Log.Warn("the settings page could not be served: %v", err)
	} else {
		logging.Log.Info("settings page at %s", url)
		if !state.Config().Configured() {
			// There is no icon to ask for the page, and nothing is written down
			// yet, so it is opened as well as logged.
			if err := platform.OpenExternal(url); err != nil {
				logging.Log.Warn("opening the settings page: %v", err)
			}
		}
	}

	runHeadless(state, stop)
}

func versionLine() string {
	return fmt.Sprintf("SDAutoLogin %s (%s/%s)", app.FullVersion(), app.PlatformName(), app.Arch())
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

// autostartArguments is what an autostart entry has to repeat from this run. A
// --home was given to say where the settings and logs live, and an entry without
// it would start on a fresh configuration beside the executable, which looks from
// the outside like the account was lost; it is made absolute because the entry
// does not run in this working directory.
//
// Nothing else is repeated, and in particular no password: an entry outlives the
// run, in a registry value or a file anybody can read.
func autostartArguments(opts options) []string {
	if opts.home == nil || *opts.home == "" {
		return nil
	}
	dir, err := filepath.Abs(*opts.home)
	if err != nil {
		dir = *opts.home
	}
	return []string{"--home=" + dir}
}

// runHeadless keeps checking in the background with no tray. stop, when not nil,
// ends it early; the settings page quits a background run this way.
//
// It never asks anything on stdin: a background run is started by a service
// manager or a container, where the questions of a wizard would either block the
// check loop or be answered by end of file, and the settings page is the way in
// for a machine that has no account yet. `config` is the command that asks.
func runHeadless(state *app.AppState, stop <-chan struct{}) {
	cfg := state.Config()
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
