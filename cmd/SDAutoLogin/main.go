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
	"sdautologin/internal/update"

	// The Windows icon, version information and application manifest live in
	// that package as a linked object file, so it has to be part of the build.
	_ "sdautologin/winres"
)

func main() {
	args := os.Args[1:]
	// Before anything else runs: an update renames the copy it replaces to
	// <program>.old, and that name is clearable only while the program now
	// running is not the one it names, which is the case here and nowhere
	// later. Nothing is reported when there is nothing to clear.
	if exe, err := os.Executable(); err == nil {
		update.RemoveLeftover(exe)
	}
	// The options are read before anything is opened: --home decides where
	// config.json and logs/ live, and a failure still has to be reported on a
	// console that may not exist yet, see attachConsole.
	opts, rest, err := parseOptions(args)
	appDir, dirErr := chooseAppDir(opts.home)
	// The autostart entry written later has to start the program this run is, so
	// the option deciding where the settings live is recorded here. The rest of
	// the command line is for this run only, a password included.
	platform.SetAutostartArguments(autostartArguments(opts))
	// The log is opened before the console is attached: a console that cannot be
	// attached is recorded in the log file. --no-log is known before the
	// configuration is read, so a run that asks for no log file creates none.
	if dirErr == nil {
		logging.Log.Init(filepath.Join(appDir, "logs"), !opts.noLog)
	}
	if len(args) > 0 {
		// Arguments mean a command was asked for, so a terminal is waiting for
		// the answer. See attachConsole.
		attachConsole()
	}
	if dirErr != nil {
		// No directory holds the settings, so there is no log file to write this
		// into either, and the console is the only place left for it.
		fmt.Fprintf(os.Stderr, "%v\n\n", dirErr)
		printUsage(os.Stderr)
		os.Exit(2)
	}
	logging.Log.Info("SDAutoLogin %s starting on %s, app dir %s", app.FullVersion(), app.PlatformName(), appDir)

	if err != nil {
		// The reason also goes to the log: the Windows build is linked as a GUI
		// program, so a start that fails on an argument leaves nothing on screen
		// and app.log is the only trace of it.
		logging.Log.Error("%v", err)
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printUsage(os.Stderr)
		os.Exit(2)
	}

	// The shortcuts take the same account options the commands do, so a script can
	// configure a machine without a terminal.
	command, commandArgs, err := invocation(rest)
	if err != nil {
		logging.Log.Error("%v", err)
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printUsage(os.Stderr)
		os.Exit(2)
	}
	if command != "" {
		os.Exit(runCommand(append([]string{command}, commandArgs...), appDir, opts))
	}
	if len(commandArgs) > 0 {
		// The only word that can be left here is the flag of the config command:
		// a tray run names no command, so nothing on this line would read it. It
		// is refused rather than dropped, since a reader who wrote it asked for
		// something that is not happening.
		fmt.Fprintf(os.Stderr, "%s belongs to the config command; the tray reads none of the command flags\n\n", commandArgs[0])
		printUsage(os.Stderr)
		os.Exit(2)
	}

	// The tray names no command, so an option belonging to one cannot be honoured
	// here. Refusing it says what is wrong; dropping it would leave a script
	// waiting for a report that never comes.
	if err := opts.checkTrayOptions(); err != nil {
		logging.Log.Error("%v", err)
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printUsage(os.Stderr)
		os.Exit(2)
	}
	// Only the tray mode has a window to open, so a build without a tray reports
	// the error and stops.
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
	// HasAccounts, not Configured: what brings the page up is a machine with
	// nothing written down, and a configuration whose passwords are deliberately
	// not stored has been written down. Asking whether it can sign in made every
	// start of one look like a first run.
	if !cfg.HasAccounts() {
		// Logged here alone: the tray opens the page for the same reason, and
		// logging it in both places would print it twice.
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

// runFallback keeps checking with no tray and no window, on a machine where the
// icon cannot be put up. The page is the only way into such a machine, so it is
// served here; its exit button ends the run.
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
		if addr, ok := server.Addr(); ok {
			logging.Log.Info("settings page on %s, opened in the browser", addr)
		}
		if !state.Config().HasAccounts() {
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

// shortcutCommand is the command a shortcut word stands for, and false when the
// word is not one.
func shortcutCommand(word string) (string, bool) {
	switch word {
	case "-test-login", "--test-login":
		return "login", true
	case "-cli", "--cli", "-daemon", "--daemon", "-headless":
		return "run", true
	}
	return "", false
}

// invocation says which command the command line names and with which arguments.
// The command is the first word that is not an option, wherever it was written:
// `-cli login` used to read -cli as the answer and drop login, and `-show config`
// used to start the tray without a word about the config command that was asked
// for. A shortcut stands for its command, and naming a shortcut next to a
// different command is refused rather than one of them being preferred silently.
// The words that are left over are what a command would have read, so the caller
// can refuse them instead of letting a command run without them.
func invocation(args []string) (command string, commandArgs []string, err error) {
	for _, arg := range args {
		switch arg {
		case "help", "-h", "--help", "-help":
			// Help and version describe the program itself, so one of them
			// answers whatever else was written on the line.
			return "help", nil, nil
		case "version", "-v", "--version":
			return "version", nil, nil
		}
	}

	words := make([]string, 0, len(args))
	for _, arg := range args {
		if name, ok := shortcutCommand(arg); ok {
			switch {
			case command == "":
				command = name
			case command != name:
				return "", nil, fmt.Errorf("%s stands for the %s command, and %s was asked for; name only one of them", arg, name, command)
			}
			continue
		}
		if command == "" && !strings.HasPrefix(arg, "-") {
			command = arg
			continue
		}
		if arg == command {
			// The same command named a second time, as in `-cli run`: a shortcut
			// and the command it stands for are one request, not a word the
			// command would have to read.
			continue
		}
		words = append(words, arg)
	}
	return command, words, nil
}

// resolveAppDir is chooseAppDir without the reason, for the caller that only
// wants the value.
func resolveAppDir(home *string) string {
	dir, _ := chooseAppDir(home)
	return dir
}

// chooseAppDir picks the directory holding config.json and logs/. An explicit
// --home wins, then the executable's own directory, so an unpacked copy keeps
// its settings beside it, then the per-user one. The error names both attempts.
func chooseAppDir(home *string) (string, error) {
	if home != nil && *home != "" {
		return *home, nil
	}

	exeDir, fromBuild, exeErr := executableDir()
	if exeErr == nil {
		return exeDir, nil
	}
	if fromBuild {
		// A build directory holds no installation, and `go run` and a test binary
		// are started from the working directory: that is where their settings go.
		if wd, err := os.Getwd(); err == nil && writableDir(wd) == nil {
			return wd, nil
		}
	}

	userDir, userErr := perUserDir()
	if userErr == nil {
		return userDir, nil
	}
	return "", fmt.Errorf("no directory to keep config.json and logs in: %v, and %v; pass --home=DIR to name one",
		exeErr, userErr)
}

// executableDir is the directory holding the program, and an error naming what
// keeps it from holding the settings as well. fromBuild says the directory holds
// a build, which the caller answers with the working directory.
func executableDir() (dir string, fromBuild bool, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", false, fmt.Errorf("the program cannot say where it is: %w", err)
	}
	dir = filepath.Dir(exe)
	if strings.Contains(dir, "go-build") || strings.Contains(dir, "Temp") {
		return dir, true, fmt.Errorf("%s holds a build, not an installation", dir)
	}
	if systemBinDir(dir) {
		return dir, false, fmt.Errorf("%s belongs to the system, the program in it came from a package", dir)
	}
	if err := writableDir(dir); err != nil {
		return dir, false, fmt.Errorf("%s cannot be written in: %w", dir, err)
	}
	return dir, false, nil
}

// systemBinDir reports whether dir is one of the directories a package installs
// its programs into. A run as root can write there, and the settings of a service
// or of a desktop user belong in the per-user directory, not on the system path.
func systemBinDir(dir string) bool {
	clean := filepath.Clean(dir)
	for _, system := range []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin", "/usr/local/sbin"} {
		if clean == system {
			return true
		}
	}
	return false
}

// perUserDir is the directory a program that may not write next to itself keeps
// its settings in: its own directory under the per-user configuration directory,
// made private because config.json holds an account.
func perUserDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("the per-user directory is unknown: %w", err)
	}
	dir := filepath.Join(base, "sdautologin")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("%s cannot be made: %w", base, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("%s cannot be made: %w", dir, err)
	}
	// MkdirAll leaves a directory that is already there alone, and this path
	// comes from $XDG_CONFIG_HOME and $HOME, which the environment of this run
	// decides: a directory another user can read is not a place for a
	// configuration that may hold a password, so it is made private here.
	if err := makePrivate(dir); err != nil {
		return "", err
	}
	if err := writableDir(dir); err != nil {
		return "", fmt.Errorf("%s cannot be written in: %w", dir, err)
	}
	return dir, nil
}

// makePrivate keeps dir to the account this program runs as, and refuses one
// that ends up reachable by another user. What that takes differs by platform:
// the mode bits are the protection where they are real and are decorative on
// Windows, where the access list is, so the mechanism is in the file for each
// platform (makePrivate_other.go and makePrivate_windows.go) and only the check
// that the name is a directory is shared. Neither Chmod nor MkdirAll is the
// whole of it: the directory may already be there, made by an earlier run or by
// an environment that pointed the configuration somewhere else.
func makePrivate(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s cannot be read back: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return restrictToCaller(dir)
}

// writableDir reports why a file cannot be created in dir, and nil when one
// can. The mode bits are not enough: a read-only mount and an access list both
// refuse a write they allow. A directory not there yet is judged by the nearest
// one above.
func writableDir(dir string) error {
	probe := dir
	for {
		info, err := os.Stat(probe)
		if err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return fmt.Errorf("%s is not there", dir)
		}
		probe = parent
	}
	f, err := os.CreateTemp(probe, ".writable-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// autostartArguments is what an autostart entry has to repeat from this run: the
// --home that says where the settings live, made absolute because the entry does
// not run in this working directory. No password: an entry outlives the run.
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
// ends it early; the settings page quits a background run this way. Nothing is
// read from stdin; `config` and the settings page are the way in.
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
	// SIGHUP is the reload a service manager sends: systemd runs ExecReload for
	// it, and it is the only way to pick up an edited config.json without a
	// restart. The tray offers the same reload from its menu.
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	for quitting := false; !quitting; {
		select {
		case sig := <-ch:
			if sig == syscall.SIGHUP {
				reloadConfig(state)
				continue
			}
			logging.Log.Info("quit signal received")
			quitting = true
		case <-stop:
			logging.Log.Info("quit requested from the settings page")
			quitting = true
		}
	}
	state.Stop()
}

// reloadConfig applies config.json again and reports what it holds now.
func reloadConfig(state *app.AppState) {
	next, err := state.Reload()
	if err != nil {
		logging.Log.Error("reloading config: %v", err)
		return
	}
	logging.Log.Info("config reloaded: %s", next.Summary())
}
