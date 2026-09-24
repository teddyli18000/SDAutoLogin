package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"sdautologin/internal/app"
	"sdautologin/internal/config"
	"sdautologin/internal/i18n"
	"sdautologin/internal/logging"
	"sdautologin/internal/platform"
	"sdautologin/internal/portal"
)

// The command line stays English on purpose: it is used from servers, from
// scripts and next to the log output, all of which are English as well.

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `SDAutoLogin %s - campus network auto login

Usage:
  SDAutoLogin [command] [options]

Without a command the tray icon starts and the settings page is served on
loopback.

Commands:
  run            run in the background, no tray
  status         print the current session state once and exit
  status --json  print the same as one line of JSON, for a monitoring script
  login          submit one login with the configured account and print the reply;
                 nothing is submitted while the portal already shows this client
                 online, since the portal allows one session per address
  config         interactive setup, writes config.json
  config --show  print the effective configuration (password never included)
  version        print the version
  help           print this text

Global option:
  --home=DIR          keep config.json and logs/ in DIR instead of next to the
                      executable, so a service or a container can point them at
                      a writable directory
  --no-log            keep the log on the console alone instead of also writing
                      logs/app.log. A machine with a small flash wants this: its
                      syslog already keeps what the program writes to the
                      console, and the file would be the same text on the flash
                      a second time

Options, for the commands that read the account:
  --account=USER:PASSWORD[@SERVICE[+SERVICE...]]
                      one entry of the login list, and the option may be
                      repeated: the order they are given in is the order they are
                      tried, and the first one that signs in is the one the
                      session keeps. A service is a name such as zgyd, the @ of
                      the portal's own spelling being optional; leaving the part
                      after the password out means the campus network itself. The
                      @ separates the password from the services, so a password
                      that holds one has to be given with --username and
                      --password instead.
  --username=NAME     use this account, as the single entry of the list
  --password=SECRET   use this password instead of the configured one
  --password-file=PATH
                      read the password from the first line of PATH instead. A
                      secret given on the command line is in the process list of
                      every user on the machine, so this is the form to use from
                      a script, a service or a container. A systemd unit that
                      hands the secret over with LoadCredential needs neither:
                      the program reads $CREDENTIALS_DIRECTORY/password itself
  --isp=SUFFIX        service suffix such as @zgyd, an empty value for the
                      campus network itself
  --save              write what was given here into config.json as well,
                      instead of using it for this run only

Option for the tray mode:
  --app[=ADDR]        open the settings page in a window of its own, without the
                      browser around it; ADDR is where to serve it, and leaving
                      it out keeps it on loopback. Needs a browser of the
                      Chromium family, and falls back to the default browser

Option for the run command:
  --settings=ADDR     also serve the settings page on ADDR, for example
                      127.0.0.1:8088 or 0.0.0.0:8088, so a machine with no tray
                      and no display can still be configured from a browser.
                      The address to open, token and all, is the one the log
                      prints; the bare port is refused

Option for the status command:
  --json              print one line of JSON instead of the report; the exit
                      codes are the same either way

An option may also be written as --name value, but the form above is the one to
use when the value holds a character the shell would otherwise act on, such as
the @ of a service suffix.

Options are read from the command line, so a container or a service without a
terminal can be provisioned without running the setup wizard.

Shortcuts:
  -cli, -daemon   same as the run command
  -test-login     same as the login command

Files:
  config.json and logs/ live next to the executable, or in the directory named
  by --home.
`, app.Version)
}

// options holds the values the command line may override, so a machine with no
// one at the keyboard can be configured without the wizard or the settings page.
type options struct {
	// home is the directory that holds config.json and logs/, when it is not
	// the one next to the executable.
	home     *string
	username *string
	password *string
	// passwordFile is a path to read the password from instead of naming it
	// here. It is for the machines where the secret belongs in neither the config
	// file nor the command line, the command line being readable by every user
	// that can see the process list.
	passwordFile *string
	isp          *string
	// accounts are entries given whole, written as user:password@service, in the
	// order they are to be tried. A provisioning script fills the login list by
	// repeating the option.
	accounts []string
	// settings is the address to serve the settings page on, for the run
	// command; nil leaves the page off.
	settings *string
	// app is the address to serve the settings page on for the tray mode, nil
	// when the settings page is only opened on demand. An empty address means
	// loopback on a port the system picks.
	app *string
	// save also writes the overrides to config.json.
	save bool
	// json prints the status report as JSON instead of text.
	json bool
	// noLog keeps the log on the console alone, for the machines that have no
	// room for it on the flash.
	noLog bool
}

// knownOption reports whether arg is one of the options this program defines.
// This tells a forgotten value apart from a value that merely starts with a
// dash, such as a password.
func knownOption(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	switch name {
	case "--home", "--username", "--password", "--password-file", "--isp", "--account", "--settings",
		"--app", "--save", "--json", "--no-log":
		return true
	}
	return false
}

// takesValue reports whether the option needs a value of its own. --app is not
// one of them: an address is optional there, and leaving it out is the common
// case on a desktop.
func takesValue(name string) bool {
	switch name {
	case "--home", "--username", "--password", "--password-file", "--isp", "--account", "--settings":
		return true
	}
	return false
}

// knownWord reports whether arg is one of the arguments that start with a dash
// without being an option this program reads: the shortcuts, and the flag the
// config command takes. They are left in the argument list for the command to
// pick up, so a misspelled option can be told apart from them.
func knownWord(arg string) bool {
	switch arg {
	case "-h", "--help", "-help", "-v", "--version",
		"-test-login", "--test-login",
		"-cli", "--cli", "-daemon", "--daemon", "-headless",
		"-show", "--show":
		return true
	}
	return false
}

// set records one option, reporting false when it is not one of ours.
func (o *options) set(name, value string) bool {
	switch name {
	case "--home":
		v := value
		o.home = &v
	case "--username":
		v := value
		o.username = &v
	case "--password":
		v := value
		o.password = &v
	case "--password-file":
		v := value
		o.passwordFile = &v
	case "--isp":
		v := value
		o.isp = &v
	case "--account":
		o.accounts = append(o.accounts, value)
	case "--settings":
		v := value
		o.settings = &v
	case "--app":
		v := value
		o.app = &v
	case "--save":
		o.save = true
	case "--json":
		o.json = true
	case "--no-log":
		o.noLog = true
	default:
		return false
	}
	return true
}

// parseOptions pulls the options out of args and returns what is left. Every
// option is accepted as --name=value or as --name value.
//
// An argument that starts with a dash, is not an option and is not one of the
// known words is a typo, and it is reported instead of being passed on: a
// monitoring script that writes --jason would otherwise be told the session
// state it never asked about, and read it as an answer.
func parseOptions(args []string) (options, []string, error) {
	var opts options
	rest := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if !knownOption(name) {
			if strings.HasPrefix(args[i], "-") && !knownWord(args[i]) {
				return opts, nil, fmt.Errorf("unknown option %q", args[i])
			}
			rest = append(rest, args[i])
			continue
		}
		if takesValue(name) && !hasValue {
			if i+1 >= len(args) || knownOption(args[i+1]) {
				return opts, nil, fmt.Errorf("%s needs a value", name)
			}
			i++
			value = args[i]
		}
		opts.set(name, value)
	}
	return opts, rest, nil
}

// given reports whether anything was overridden on the command line.
func (o options) given() bool {
	return o.accountGiven() || o.noLog
}

// accountGiven reports whether one of the account options was used. Only then is
// the reminder about --save printed: that reminder is about a secret used for one
// run only, while --no-log is a switch meant to be passed on every start.
func (o options) accountGiven() bool {
	return o.username != nil || o.password != nil || o.passwordFile != nil ||
		o.isp != nil || len(o.accounts) > 0
}

// applyTo copies the overrides onto cfg. An option that was not given leaves the
// configured value alone, so one secret can be passed without repeating the rest.
//
// The account options describe the login list as a whole rather than adding to
// it: a provisioning script names the accounts to use, in the order it names them.
func (o options) applyTo(cfg *config.Config) error {
	// The password may come from a file rather than from here, which is how a
	// service or a container is given one without it appearing in a process list.
	password, givenPassword, err := o.passwordOverride()
	if err != nil {
		return err
	}

	switch {
	case len(o.accounts) > 0 && (o.username != nil || o.password != nil || o.passwordFile != nil || o.isp != nil):
		return fmt.Errorf("--account names the account and the service itself, so it cannot be combined with --username, --password, --password-file or --isp")

	case len(o.accounts) > 0 && givenPassword:
		return fmt.Errorf("--account carries the password itself, so it cannot be combined with a password read from a file")

	case len(o.accounts) > 0:
		list := make([]config.Account, 0, len(o.accounts))
		for _, raw := range o.accounts {
			entry, err := parseAccount(raw)
			if err != nil {
				return err
			}
			list = append(list, entry)
		}
		cfg.Accounts = list

	case o.username != nil || o.password != nil || o.passwordFile != nil || o.isp != nil || givenPassword:
		// One account described field by field. What is not given is taken from
		// the entry that is already there, so a password can be changed without
		// repeating the account it belongs to.
		var entry config.Account
		if len(cfg.Accounts) > 0 {
			entry = cfg.Accounts[0]
		}
		if o.username != nil {
			entry.Username = *o.username
		}
		if givenPassword {
			entry.Password = password
		}
		if o.isp != nil {
			entry.ISPs = []string{*o.isp}
		}
		cfg.Accounts = []config.Account{entry}
	}

	if o.noLog {
		cfg.LogToFile = false
	}
	return nil
}

// passwordOverride is the password to use in place of the configured one, and
// whether there is one to use at all. It is named on the command line, or read
// from a file, or handed over by systemd; a run that was given none of them keeps
// what the configuration holds, which is what --save then writes.
func (o options) passwordOverride() (string, bool, error) {
	switch {
	case o.password != nil && o.passwordFile != nil:
		return "", false, fmt.Errorf("--password and --password-file both name the password")
	case o.password != nil:
		return *o.password, true, nil
	case o.passwordFile != nil:
		password, err := readPasswordFile(*o.passwordFile)
		if err != nil {
			return "", false, err
		}
		return password, true, nil
	}

	systemd := credentialPasswordPath()
	if systemd == "" {
		return "", false, nil
	}
	password, err := readPasswordFile(systemd)
	if err != nil {
		return "", false, err
	}
	return password, true, nil
}

// credentialName is the name of the credential a systemd unit is expected to hand
// over: LoadCredential=password:/some/file puts that file at
// $CREDENTIALS_DIRECTORY/password, which no other process on the machine can
// read, and which is not written to disk again by anything here.
const credentialName = "password"

// credentialPasswordPath is where systemd left the credential for this run, and
// "" when there is none — which is every run systemd did not start at all, and
// every unit that does not ask for a credential.
func credentialPasswordPath() string {
	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, credentialName)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return ""
	}
	return path
}

// readPasswordFile reads the password from the first line of a file, which is how
// a secret is kept out of both a command line and the configuration file. Only
// the line ending is dropped: a password may end in a space, and trimming more
// than the newline would quietly change it.
func readPasswordFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the password from %s: %w", path, err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		return "", fmt.Errorf("%s holds no password", path)
	}
	return line, nil
}

// parseAccount reads the one line form of an account,
// user:password@service[+service...], for a script that sets up more than one
// account.
//
// The @ always introduces the services, so an account with a password that holds
// one has to be given with --username and --password instead. The services are
// separated by +, the @ of the portal's own spelling in front of each being
// optional, and an empty element is the campus network itself. Leaving the part
// after the password out entirely means the campus network as well.
//
// A value with more than one @ cannot be read at all and is refused: it would be
// a guess whether the last one starts a service or belongs to the password. The
// value is never echoed back in an error, since it holds the password.
func parseAccount(raw string) (config.Account, error) {
	if strings.Count(raw, "@") > 1 {
		return config.Account{}, fmt.Errorf("--account takes one @ at most, the one that introduces the services; a password holding another needs --username and --password")
	}
	credentials, services := raw, ""
	if i := strings.Index(raw, "@"); i >= 0 {
		credentials, services = raw[:i], raw[i+1:]
	}
	username, password, ok := strings.Cut(credentials, ":")
	if !ok || strings.TrimSpace(username) == "" {
		return config.Account{}, fmt.Errorf("--account is written as user:password@service, and this value is not")
	}

	entry := config.Account{Username: strings.TrimSpace(username), Password: password}
	if services != "" {
		for _, part := range strings.Split(services, "+") {
			part = strings.TrimSpace(part)
			if part == "" {
				entry.ISPs = append(entry.ISPs, "")
				continue
			}
			entry.ISPs = append(entry.ISPs, "@"+strings.TrimPrefix(part, "@"))
		}
	}
	return entry, nil
}

// check reports an option that does not belong to the command that was asked
// for, rather than letting it be silently ignored.
func (o options) check(command string) error {
	// help and version describe the program itself and read no options at all,
	// so they answer whatever else was written on the command line.
	if command == "help" || command == "version" {
		return nil
	}
	switch {
	case o.settings != nil && command != "run" && command != "daemon":
		return fmt.Errorf("--settings belongs to the run command, %s does not serve a settings page", command)
	case o.app != nil:
		// The tray is the only mode with a window, and it is started by naming
		// no command at all.
		return fmt.Errorf("--app belongs to the tray mode, %s does not open a window", command)
	case o.json && command != "status":
		return fmt.Errorf("--json belongs to the status command, %s has nothing to report as JSON", command)
	}
	return nil
}

// checkTray reports an option that needs a tray icon in a build that has none.
// The tray opens a window of its own, so a build for machines with no desktop
// session cannot honour --app. Refusing it is better than ignoring it after start.
func checkTray(o options, hasTray bool) error {
	if o.app != nil && !hasTray {
		return fmt.Errorf("--app needs the tray, which this build does not have")
	}
	return nil
}

// runCommand runs the command named by args[0]. The options have already been
// taken out of args, since --home has to be known before the directory holding
// config.json and logs/ can be resolved.
func runCommand(args []string, appDir string, opts options) int {
	command := args[0]
	if err := opts.check(command); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printUsage(os.Stderr)
		return 2
	}

	switch command {
	case "run", "daemon":
		return cmdRun(appDir, opts)
	case "status":
		return cmdStatus(appDir, opts)
	case "login":
		return cmdLogin(appDir, opts)
	case "config":
		return cmdConfig(appDir, args[1:], opts)
	case "version":
		fmt.Println(versionLine())
		return 0
	case "help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", command)
		printUsage(os.Stderr)
		return 2
	}
}

// loadForCLI reads config.json, applies whatever the command line overrode and
// sets up the logging and language the configuration asks for. It reports a hard
// failure so commands can return a non zero exit code.
func loadForCLI(appDir string, opts options) (*config.Config, bool) {
	cfg, err := config.LoadConfig(filepath.Join(appDir, "config.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return cfg, false
	}

	if opts.given() {
		if err := opts.applyTo(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return cfg, false
		}
		if opts.save {
			// Save drops the passwords when they are not remembered, and the
			// caller explicitly asked for these values to be written down.
			if opts.password != nil || len(opts.accounts) > 0 {
				cfg.RememberPassword = true
			}
			if err := cfg.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "writing the config: %v\n", err)
				return cfg, false
			}
			fmt.Fprintf(os.Stderr, "config updated: %s\n", cfg.Path())
		} else if opts.accountGiven() {
			fmt.Fprintln(os.Stderr, "note: the values given on the command line are used for this run only, add --save to write them to the config file")
		}
	}

	i18n.SetLang(i18n.Lang(cfg.Language))
	logging.Log.SetVerbose(cfg.VerboseLog)
	if !cfg.LogToFile {
		// The log file was opened before the configuration could be read, since
		// a failure to read it has to be logged as well, so it is closed here
		// when the configuration says it is not wanted.
		logging.Log.CloseFile()
	}
	return cfg, true
}

// checkAccountForRun reports whether a background run may start with what it was
// given. A service has nobody to answer the setup wizard, so a run with nothing
// to sign in with is refused rather than started into a loop that can only
// report the same failure; the exception is a run that serves the settings page,
// because that page is the way in on a machine whose operator has no other one.
func checkAccountForRun(cfg *config.Config, opts options) error {
	if cfg.Configured() || opts.settings != nil {
		return nil
	}
	return errors.New("no account configured: pass --username and --password, or --account, or run the config command first")
}

func cmdRun(appDir string, opts options) int {
	cfg, ok := loadForCLI(appDir, opts)
	if !ok {
		return 1
	}
	if err := checkAccountForRun(cfg, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !platform.AcquireSingleInstance(false) {
		fmt.Fprintln(os.Stderr, "another instance is already running")
		return 1
	}

	state := app.New(cfg)
	stop := make(chan struct{})
	var settingsServer *app.Server

	if opts.settings != nil {
		// The page does not need a tray, so a machine with no display can be
		// configured from a browser the same way a desktop is.
		server := app.NewServer(state, *opts.settings, func() {
			// The page asked the program to exit, which ends a background run
			// the same way a signal does.
			close(stop)
		})
		defer server.Stop()
		settingsServer = server

		url, err := server.URL()
		if err != nil {
			fmt.Fprintf(os.Stderr, "settings page: %v\n", err)
			return 1
		}
		if !cfg.Configured() {
			// This run was allowed to start with nothing to sign in with
			// because of the page, so it says where the page is: the checks
			// will report the missing account until one is written there.
			logging.Log.Warn("no account is written down yet, write one at %s", url)
		}
	}

	// A second copy is told where the page is, or that there is none, so the
	// request is answered rather than dropped in silence. The address is read
	// when it is asked for rather than kept: the page is opened with one that
	// works once, and the one it is replaced by is the one to hand out now.
	go platform.WaitForSettingsRequest(func() {
		if settingsServer == nil {
			logging.Log.Warn("another copy asked for the settings page, which the background mode does not serve")
			return
		}
		url, err := settingsServer.URL()
		if err != nil {
			logging.Log.Warn("another copy asked for the settings page, and its address could not be made: %v", err)
			return
		}
		logging.Log.Info("another copy asked for the settings page, it is at %s", url)
	})

	runHeadless(state, stop)
	return 0
}

func cmdStatus(appDir string, opts options) int {
	cfg, ok := loadForCLI(appDir, opts)
	if !ok {
		return 1
	}
	st := portal.NewClient(cfg).QueryStatus()

	if opts.json {
		return writeStatusJSON(cfg.PortalBaseURL, st)
	}

	fmt.Printf("portal       : %s\n", cfg.PortalBaseURL)
	fmt.Printf("reachable    : %v\n", st.Reachable)
	fmt.Printf("state_known  : %v\n", st.Known)
	fmt.Printf("online       : %v\n", st.Online)
	fmt.Printf("client_ip    : %s\n", blank(st.ClientIP))
	fmt.Printf("account      : %s\n", blank(st.Account))
	fmt.Printf("online_since : %s\n", blank(st.OnlineSince))
	fmt.Printf("detail       : %s\n", st.Message)

	return statusExitCode(st)
}

// statusReport is the machine readable form of the status command. Where a field
// also exists in the settings API it keeps that name.
type statusReport struct {
	Portal      string `json:"portal"`
	State       string `json:"state"`
	Reachable   bool   `json:"reachable"`
	Known       bool   `json:"known"`
	Online      bool   `json:"online"`
	ClientIP    string `json:"clientIp"`
	Account     string `json:"account"`
	OnlineSince string `json:"onlineSince"`
	Detail      string `json:"detail"`
}

// The four things a single check can find.
const (
	// The portal did not answer, so nothing is known about the session.
	stateUnreachable = "unreachable"
	// It answered, but the reply said nothing usable about this client.
	stateUnknown = "unknown"
	stateOnline  = "online"
	// The portal says this client is not signed in.
	stateOffline = "offline"
)

func newStatusReport(portalBaseURL string, st portal.Status) statusReport {
	return statusReport{
		Portal:      portalBaseURL,
		State:       statusState(st),
		Reachable:   st.Reachable,
		Known:       st.Known,
		Online:      st.Online,
		ClientIP:    st.ClientIP,
		Account:     st.Account,
		OnlineSince: st.OnlineSince,
		Detail:      st.Message,
	}
}

// statusState names what the check found, finer grained than the exit code: a
// portal that cannot be reached and one that answers nothing useful are both
// exit code 1, but they are a different story to tell.
func statusState(st portal.Status) string {
	switch {
	case !st.Reachable:
		return stateUnreachable
	case !st.Known:
		return stateUnknown
	case st.Online:
		return stateOnline
	default:
		return stateOffline
	}
}

// statusExitCode is the code a monitoring script reads: 0 online, 1 unknown or a
// failure to ask the portal, 3 the portal says this client is not signed in.
func statusExitCode(st portal.Status) int {
	switch statusState(st) {
	case stateOnline:
		return 0
	case stateOffline:
		return 3
	default:
		return 1
	}
}

// writeStatusJSON prints the report as a single line, so a script can read it
// without a parser that spans lines. The exit code is the same as for the text
// form, so either can be used to watch the session.
func writeStatusJSON(portalBaseURL string, st portal.Status) int {
	err := json.NewEncoder(os.Stdout).Encode(newStatusReport(portalBaseURL, st))
	if err != nil {
		fmt.Fprintf(os.Stderr, "writing the status: %v\n", err)
		return 1
	}
	return statusExitCode(st)
}

func cmdLogin(appDir string, opts options) int {
	cfg, ok := loadForCLI(appDir, opts)
	if !ok {
		return 1
	}
	attempts := cfg.Attempts()
	if len(attempts) == 0 {
		fmt.Fprintln(os.Stderr, "no account configured, run: SDAutoLogin config")
		return 1
	}
	// A one shot test is about the first entry, the one the program tries first.
	// The settings page can test any of them.
	attempt := attempts[0]
	test := app.New(cfg).TestLogin(attempt)
	fmt.Printf("before       : online=%v ip=%s account=%s\n",
		test.Before.Online, blank(test.Before.ClientIP), blank(test.Before.Account))
	if test.AlreadyOnline {
		fmt.Println("submitted    : nothing, the portal already reports this client as online")
		fmt.Printf("after        : online=%v ip=%s account=%s\n",
			test.After.Online, blank(test.After.ClientIP), blank(test.After.Account))
		// Nothing is wrong: the session is up. Testing a different account
		// would need the session down, and this program never signs one out.
		return 0
	}
	fmt.Printf("submitted    : %s (password %d chars, entry %d of %d)\n",
		attempt.Entry(), len(attempt.Password), attempt.Index+1, len(cfg.Accounts))
	fmt.Printf("success      : %v\n", test.Result.Success)
	fmt.Printf("ret_code     : %s\n", blank(test.Result.RetCode))
	fmt.Printf("message      : %s\n", test.Result.Message)
	fmt.Printf("raw_reply    : %s\n", portal.Truncate(test.Result.Raw))
	fmt.Printf("after        : online=%v ip=%s account=%s\n",
		test.After.Online, blank(test.After.ClientIP), blank(test.After.Account))

	if !test.Result.Success {
		return 1
	}
	return 0
}

func cmdConfig(appDir string, rest []string, opts options) int {
	cfg, ok := loadForCLI(appDir, opts)
	if !ok {
		return 1
	}
	for _, arg := range rest {
		if arg == "--show" || arg == "-show" {
			fmt.Printf("config_file : %s\n", cfg.Path())
			fmt.Printf("summary     : %s\n", cfg.Summary())
			return 0
		}
	}
	runSetupWizard(cfg)
	return 0
}

func blank(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
