package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"sdautologin/internal/app"
	"sdautologin/internal/config"
	"sdautologin/internal/i18n"
	"sdautologin/internal/logging"
	"sdautologin/internal/platform"
	"sdautologin/internal/portal"
	"sdautologin/internal/update"
)

// The command line stays English: it is used from servers, from scripts and
// next to the log output, all of them English.

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `SDAutoLogin %s - campus network auto login

Usage:
  SDAutoLogin [command] [options]

Without a command the tray icon starts and the settings page is served on
loopback.

Commands:
  run            run in the background, no tray
  status         print the current session state once and exit; the exit code is
                 the answer: 0 online, 1 the portal could not be asked or said
                 nothing usable, 3 the portal says this client is not signed in
  status --json  print the same as one line of JSON, for a monitoring script
  update         look for a newer release of this program and print what was
                 found. This is the manual check, so it asks whatever
                 checkForUpdates says; updateChannel still decides which
                 releases it is offered. It also says when this build carries no
                 signing key, and then installs nothing
  update --install
                 install the release it found, when this copy can replace itself
  update --json  print the same as one line of JSON, for a script
  login          submit one login with the configured account and print the reply;
                 nothing is submitted while the portal already shows this client
                 online, since the portal allows one session per address
  config         interactive setup, writes config.json
  config --show  print the effective configuration (password never included)
  version        print the version
  help           print this text

Exit codes:
  0   the portal reports this client online (status), or the update check ran
      (update)
  1   the portal could not be asked or answered nothing usable (status), the
      release page could not be asked or what was asked for could not be done
      (update), or the command failed for another reason
  2   the command line was refused: an unknown option, a missing value, an option
      that belongs to another command, an unknown command
  3   the portal says this client is not signed in (status)

Global option:
  --home=DIR          keep config.json and logs/ in DIR instead of next to the
                      executable, so a service or a container can point them at
                      a writable directory
  --no-log            keep the log on the console alone instead of also writing
                      logs/app.log. A machine with a small flash wants this: its
                      syslog already keeps what the program writes to the
                      console, and the file would be the same text on the flash
                      a second time
  --quiet             leave out the per check detail and keep the lines that
                      report a change of state. A service whose console goes into
                      the system log wants this: the detail of a check every
                      fifteen seconds fills a router's log ring within the hour
                      and pushes every other service's lines out of it

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
  --password=SECRET   use this password instead of the configured one, which
                      without --username is the first entry of the list
  --password-file=PATH
                      read the passwords from PATH instead, in the two shapes
                      the passwordFile setting takes: no colon on any line is
                      one password, for the first entry of the list, and a
                      colon is one account:password per line. A secret given on
                      the command line is in the process list of every user on
                      the machine, so this is the form to use from a script, a
                      service or a container. A systemd unit that hands the
                      secret over with LoadCredential needs neither: the
                      program reads $CREDENTIALS_DIRECTORY/password itself
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

Option for the update command:
  --install           put the release the check found in place, when this build
                      can check what it downloads and this copy can replace
                      itself. Without the option nothing is downloaded

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

// options holds the values the command line may override. A machine with nobody
// at the keyboard can be configured without the wizard or the settings page.
type options struct {
	// home is the directory that holds config.json and logs/, when it is not
	// the one next to the executable.
	home     *string
	username *string
	password *string
	// passwordFile is a path to read the password from, for the machines where the
	// secret belongs in neither the config file nor the command line: every user
	// that can see the process list can read the command line.
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
	// install has the update command put the release it found in place.
	install bool
	// noLog keeps the log on the console alone, for the machines that have no
	// room for it on the flash.
	noLog bool
	// quiet drops the per check detail, which a service that writes its console
	// into the system log otherwise fills that log with.
	quiet bool
}

// knownOption reports whether arg is one of the options this program defines.
// That tells a forgotten value apart from a value that merely starts with a
// dash, such as a password.
func knownOption(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	switch name {
	case "--home", "--username", "--password", "--password-file", "--isp", "--account", "--settings",
		"--app", "--save", "--json", "--install", "--no-log", "--quiet":
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

// flagOption reports whether name is a switch that carries no value of its own.
// Such a switch may still be written with one: --save=false is how a script says
// the switch is off, and it used to be read as --save.
func flagOption(name string) bool {
	switch name {
	case "--save", "--json", "--install", "--no-log", "--quiet":
		return true
	}
	return false
}

// flagValue reads the value one of those switches was written with. Only the
// four spellings a script writes are accepted, and anything else is refused
// rather than read as "on": --save=false wrote the password into config.json,
// which is the opposite of what it says.
func flagValue(name, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("%s takes no value, or true or false after an '='", name)
}

// optionNameOf is what an error may carry about an argument this program does not
// read: the option name alone, cut before the '=' so a mistyped --pasword=SECRET
// is reported as --pasword, and bounded so a long argument cannot be copied into
// a log that outlives the run. An argument without an '=' is its own name.
func optionNameOf(arg string) string {
	name, _, _ := strings.Cut(arg, "=")
	const limit = 48
	if len(name) > limit {
		name = name[:limit] + "..."
	}
	return name
}

// knownWord reports whether arg starts with a dash without being an option this
// program reads: the shortcuts and the flag the config command takes. They are
// left in the argument list so a misspelled option can be told apart from them.
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

// set records one option that carries a value, reporting false when it is not
// one of ours. The switches that carry none are recorded by setFlag, which reads
// the value a switch was written with instead of taking it as "on".
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
	default:
		return false
	}
	return true
}

// setFlag records one of the switches that carry no value of their own, on or
// off, reporting false when name is not one of them.
func (o *options) setFlag(name string, on bool) bool {
	switch name {
	case "--save":
		o.save = on
	case "--json":
		o.json = on
	case "--install":
		o.install = on
	case "--no-log":
		o.noLog = on
	case "--quiet":
		o.quiet = on
	default:
		return false
	}
	return true
}

// parseOptions pulls the options out of args and returns what is left.
// Each option is accepted as --name=value or as --name value. An argument that
// starts with a dash and is not a known word is a typo, and it is reported by its
// option name alone: the argument may be a misspelled --password, and the log
// outlives the token and is copied around. A lone -- ends the options, so a word
// after it is never read as one.
func parseOptions(args []string) (options, []string, error) {
	var opts options
	rest := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			// Everything after it is a word: this is how an argument that begins
			// with a dash is handed to a command that takes one.
			rest = append(rest, args[i+1:]...)
			break
		}
		name, value, hasValue := strings.Cut(args[i], "=")
		if !knownOption(name) {
			// A lone - is what a reader writes in place of an argument that is
			// missing, not a typo to report.
			if args[i] != "-" && strings.HasPrefix(args[i], "-") && !knownWord(args[i]) {
				return opts, nil, fmt.Errorf("unknown option %q", optionNameOf(args[i]))
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
		if name == "--home" && strings.HasPrefix(value, "-") {
			// A directory named like an option is what a forgotten value makes:
			// --home -cli would keep the settings in a directory called -cli. The
			// absolute path, or ./ in front of it, is what says otherwise.
			return opts, nil, fmt.Errorf("--home needs a directory name, and one starting with a dash is read as another option; write it as an absolute path, or with ./ in front of it")
		}
		if flagOption(name) {
			on := true
			if hasValue {
				parsed, err := flagValue(name, value)
				if err != nil {
					return opts, nil, err
				}
				on = parsed
			}
			opts.setFlag(name, on)
			continue
		}
		opts.set(name, value)
	}
	return opts, rest, nil
}

// given reports whether this run has anything to apply on top of the
// configuration: an option on the command line, or a password systemd
// handed over to a unit that names no account itself.
func (o options) given() bool {
	return o.accountGiven() || o.noLog || o.quiet || credentialPasswordPath() != ""
}

// accountGiven reports whether one of the account options was used. Only then is
// the reminder about --save printed: that reminder is about a secret used for one
// run only, while --no-log is a switch meant to be passed on every start.
func (o options) accountGiven() bool {
	return o.username != nil || o.password != nil || o.passwordFile != nil ||
		o.isp != nil || len(o.accounts) > 0
}

// secretOnTheCommandLine reports whether an account option carries a password.
// --account holds the account and the password in the same text, so the entry has
// to be read to tell them apart: an account named on its own is not a secret.
func (o options) secretOnTheCommandLine() bool {
	if o.password != nil {
		return true
	}
	for _, raw := range o.accounts {
		if entry, err := parseAccount(raw); err == nil && entry.Password != "" {
			return true
		}
	}
	return false
}

// applyTo copies the overrides onto cfg. An option that was not given leaves
// the configured value alone. The account options replace the login list: a
// provisioning script names the accounts to use, in the order it names them.
func (o options) applyTo(cfg *config.Config) error {
	switch {
	// --account with --password is answered by the first case as well: it names
	// the account whole and its password a second time, which is what that
	// message is about. A third case listing only --password was never reached.
	case len(o.accounts) > 0 && (o.username != nil || o.password != nil || o.passwordFile != nil || o.isp != nil):
		return fmt.Errorf("--account names the account and the service itself, so it cannot be combined with --username, --password, --password-file or --isp")

	case o.password != nil && o.passwordFile != nil:
		return fmt.Errorf("--password and --password-file both name the password")
	}

	switch {
	case len(o.accounts) > 0:
		list := make([]config.Account, 0, len(o.accounts))
		for _, raw := range o.accounts {
			entry, err := parseAccount(raw)
			if err != nil {
				return err
			}
			// A password written into the option belongs to this run, and --save
			// writes it down for the runs that follow.
			if entry.Password != "" {
				entry.PasswordSource = config.SourceRun
			}
			list = append(list, entry)
		}
		cfg.Accounts = list

	case o.username != nil || o.isp != nil:
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
		if o.password != nil {
			entry.Password = *o.password
			entry.PasswordSource = config.SourceRun
		}
		if o.isp != nil {
			entry.ISPs = []string{*o.isp}
		}
		if len(entry.ISPs) == 0 {
			// An account named on its own has no service type yet, and the
			// campus network is the one the portal's dropdown starts with.
			entry.ISPs = []string{""}
		}
		cfg.Accounts = []config.Account{entry}

	case o.password != nil:
		// Only a password: a service is handed its secret without writing the
		// account out again. It belongs to the entry already first, and the rest of
		// the list is left alone.
		if len(cfg.Accounts) == 0 {
			cfg.Accounts = []config.Account{{Password: *o.password, PasswordSource: config.SourceRun, ISPs: []string{""}}}
		} else {
			cfg.Accounts[0].Password = *o.password
			cfg.Accounts[0].PasswordSource = config.SourceRun
		}
	}

	// A file named here or handed over by systemd becomes the configuration's own
	// for this run, and --save writes the path down and not the secret. A file the
	// configuration names is only reported when it cannot be read.
	//
	// The password systemd handed over belongs to this run and not to the
	// account: the path stays in the environment it was read from, and --save
	// writes down the path --password-file named and never this one. Whichever
	// one is used is named on the terminal, since a reader looking at config.json
	// would otherwise not see why another secret is in use than the one there.
	credential := credentialPasswordPath()
	configuredPasswordFile := cfg.PasswordFile
	fromCredential := false
	named := false
	switch {
	case len(o.accounts) > 0 && credential != "":
		return fmt.Errorf("--account carries the password itself, so it cannot be combined with a password read from a file")
	case o.passwordFile != nil:
		cfg.PasswordFile = *o.passwordFile
		named = true
	case o.password != nil:
		// Named on the command line: there is nothing to read.
	case credential != "":
		note := fmt.Sprintf("note: the password is read from %s", credential)
		if configuredPasswordFile != "" && configuredPasswordFile != credential {
			note += fmt.Sprintf(", which replaces the passwordFile %s of config.json", configuredPasswordFile)
		}
		if loose := readableByOthers(credential); loose != "" {
			note += "; " + loose
		}
		fmt.Fprintln(os.Stderr, note)
		cfg.PasswordFile = credential
		fromCredential = true
		named = true
	}
	if named {
		if notice := cfg.LoadPasswords(); notice != "" {
			return fmt.Errorf("%s", notice)
		}
	}
	if fromCredential {
		// Put back: the environment of this run is not a setting of the account,
		// and config.json keeps the path it had.
		cfg.PasswordFile = configuredPasswordFile
	}

	if o.noLog {
		cfg.LogToFile = false
	}
	if o.quiet {
		cfg.VerboseLog = false
	}
	return nil
}

// credentialName is the name of the credential a systemd unit hands over:
// LoadCredential=password:/some/file puts that file at
// $CREDENTIALS_DIRECTORY/password, unreadable by another process.
const credentialName = "password"

// credentialPasswordPath is where systemd left the credential for this run, and ""
// when there is none: every run systemd did not start, and every unit that asks for
// no credential.
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

// readableByOthers says so, in one line, when the file holding a password can be
// read by someone other than its owner, and "" when it cannot. systemd makes the
// file it hands over private; a unit or a wrapper script that made it 0644 has
// put the secret where every user of the machine can read it.
func readableByOthers(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if mode := info.Mode().Perm(); mode&0o004 != 0 {
		return fmt.Sprintf("the file is mode %#o, so any user of this machine can read the password in it", mode)
	}
	return ""
}

// parseAccount reads the one line form of an account, user:password@service
// [+service...]. The @ introduces the services, separated by + with an optional
// @ in front of each; an empty element is the campus network.
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

	// Leaving the part after the password out means the campus network, and an
	// entry with no service type cannot be submitted: the suffix is filled in
	// here, since a run without --save skips the normalization that supplies it.
	entry := config.Account{Username: strings.TrimSpace(username), Password: password, ISPs: []string{""}}
	if services != "" {
		entry.ISPs = nil
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
// for, so a stray option is refused before the command runs.
func (o options) check(command string) error {
	// help and version describe the program itself and read no options, so they
	// answer whatever else was written on the command line.
	if command == "help" || command == "version" {
		return nil
	}
	switch {
	case o.settings != nil && command != "run" && command != "daemon":
		return fmt.Errorf("--settings belongs to the run command, %s does not serve a settings page", command)
	case o.app != nil:
		// The tray is the only mode with a window, and it is started by naming
		// no command.
		return fmt.Errorf("--app belongs to the tray mode, %s does not open a window", command)
	case o.install && command != "update":
		return fmt.Errorf("--install belongs to the update command,"+
			" %s does not install a release", command)
	case o.json && command != "status" && command != "update":
		return fmt.Errorf("--json belongs to the status and update commands,"+
			" %s has nothing to report as JSON", command)
	}
	return nil
}

// checkTray reports an option that needs a tray icon in a build that has none.
// The tray opens a window of its own, so a build with no desktop session cannot
// honour --app, and refusing it is better than ignoring it after start.
func checkTray(o options, hasTray bool) error {
	if o.app != nil && !hasTray {
		return fmt.Errorf("--app needs the tray, which this build does not have")
	}
	return nil
}

// checkTrayOptions reports an option that only a command can honour. A tray run
// names no command, so --json, --install and --settings are refused here.
func (o options) checkTrayOptions() error {
	switch {
	case o.settings != nil:
		return fmt.Errorf("--settings belongs to the run command, the tray serves its page on loopback")
	case o.install:
		return fmt.Errorf("--install belongs to the update command," +
			" the tray does not install a release")
	case o.json:
		return fmt.Errorf("--json belongs to the status and update commands," +
			" the tray has nothing to report as JSON")
	}
	return nil
}

// The shortcut words are turned into the command they stand for before this is
// called, see invocation, so a shortcut and its command go through the same
// checks here.

// runCommand runs the command named by args[0]. The options have already been
// taken out of args, since --home has to be known before the directory holding
// config.json and logs/ can be resolved.
func runCommand(args []string, appDir string, opts options) int {
	command := args[0]
	// A word left after the command used to be dropped without a word: `status
	// extra` and `config --shwo` looked like they had done what was asked. The
	// one argument a command reads here is the flag of config.
	if err := checkCommandArgs(command, args[1:]); err != nil {
		logging.Log.Error("%v", err)
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printUsage(os.Stderr)
		return 2
	}
	// An address where a command belongs is what --app and --settings look like
	// when the '=' was left out, so it is named as an address wherever it is
	// refused. Writing it in front of the option it belongs to is a likely
	// mistake, and the answer should say which one it is.
	addressHint := ""
	if _, _, err := net.SplitHostPort(command); err == nil {
		addressHint = fmt.Sprintf("; an address is written with an '=', as in --app=%s or --settings=%s", command, command)
	}
	if err := opts.check(command); err != nil {
		logging.Log.Error("%v%s", err, addressHint)
		fmt.Fprintf(os.Stderr, "%v%s\n\n", err, addressHint)
		printUsage(os.Stderr)
		return 2
	}

	switch command {
	case "run", "daemon":
		return cmdRun(appDir, opts)
	case "status":
		return cmdStatus(appDir, opts)
	case "update":
		return cmdUpdate(appDir, opts)
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
		fmt.Fprintf(os.Stderr, "unknown command %q%s\n\n", command, addressHint)
		printUsage(os.Stderr)
		return 2
	}
}

// checkCommandArgs refuses what a command does not read. Nothing is dropped
// quietly: a script that wrote an argument the command never looks at is waiting
// for a report that would never come. A word that names no command of this
// program is left to the dispatch below, which reports it as what it is.
func checkCommandArgs(command string, args []string) error {
	switch command {
	case "help", "version":
		// The two describe the program itself and read nothing else.
		return nil
	case "config":
		for _, arg := range args {
			if arg == "--show" || arg == "-show" {
				continue
			}
			return fmt.Errorf("config reads --show and nothing else")
		}
		return nil
	case "run", "daemon", "status", "update", "login":
		if len(args) > 0 {
			return fmt.Errorf("%s takes no further argument", command)
		}
		return nil
	}
	return nil
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
	// What reading the password file had to say, said before the options are
	// applied: an entry it could not fill is skipped, and without this line a
	// command that finds no account gives no reason for it.
	if notice := cfg.PasswordNotice(); notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	}

	if opts.given() {
		if opts.secretOnTheCommandLine() {
			// Once per run and never per check: the process list of every user of
			// the machine holds this secret, and a reader who did not know has no
			// way to tell from the report alone.
			fmt.Fprintln(os.Stderr, "warning: the password is on this command line, where every user of this machine can read it from the process list; --password-file or $CREDENTIALS_DIRECTORY keeps it off")
		}
		if err := opts.applyTo(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return cfg, false
		}
		if opts.save {
			// Save drops the passwords when they are not remembered, and the
			// caller explicitly asked for these values to be written down,
			// whether they came from the command line or from a file.
			if opts.password != nil || opts.passwordFile != nil || len(opts.accounts) > 0 {
				cfg.RememberPassword = true
			}
			if err := cfg.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "writing the config: %v\n", err)
				return cfg, false
			}
			if report := cfg.Protect(); report != "" {
				fmt.Fprintln(os.Stderr, report)
			}
			if notice := cfg.PasswordNotice(); notice != "" {
				fmt.Fprintln(os.Stderr, notice)
			}
			fmt.Fprintf(os.Stderr, "config updated: %s\n", cfg.Path())
		} else if opts.accountGiven() {
			fmt.Fprintln(os.Stderr, "note: the values given on the command line are used for this run only, add --save to write them to the config file")
		}
	}

	i18n.SetLang(i18n.Lang(cfg.Language))
	logging.Log.SetVerbose(cfg.VerboseLog)
	if !cfg.LogToFile {
		// The log file was opened before the configuration could be read, since a
		// failure to read it has to be logged as well. It is closed here when the
		// configuration says it is not wanted.
		logging.Log.CloseFile()
	}
	return cfg, true
}

// checkAccountForRun reports whether a background run may start with what it
// was given. A service has nobody to answer the wizard, so a run with nothing
// to sign in with is refused, unless the settings page is served.
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
		logging.Log.Error("%v", err)
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !platform.AcquireSingleInstance(false) {
		logging.Log.Info("another instance is already running, so this one stops")
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
			// The page asked the program to exit. That ends a background run
			// the same way a signal does.
			close(stop)
		})
		defer server.Stop()
		settingsServer = server

		url, err := server.URL()
		if err != nil {
			// Into the log too: a port that cannot be bound, held by another program
			// or reserved by this machine, is the failure this option meets most
			// often.
			logging.Log.Error("settings page: %v", err)
			fmt.Fprintf(os.Stderr, "settings page: %v\n", err)
			return 1
		}
		if servesBeyondThisMachine(*opts.settings) {
			// The page is protected by the token in the address it is handed
			// out with, so an address that other machines can reach is worth a
			// word on the terminal the person running this is looking at: the
			// log is copied around and its reader may be a collector.
			fmt.Fprintf(os.Stderr, "warning: %s is not loopback, so the settings page is reachable from the network; the token in the address is all that protects it, so keep the port on a trusted network or in front of a tunnel\n", *opts.settings)
		}
		// The address goes to the terminal the person running this is looking
		// at, and not into the log: it carries the one time token, and the log
		// outlives the token and is copied around. It is said whether or not an
		// account is written down, since a machine that already has one is
		// exactly the machine whose page is opened later to change it.
		fmt.Fprintf(os.Stderr, "settings page at %s\n", url)
		if !cfg.Configured() {
			// This run was allowed to start with nothing to sign in with
			// because of the page, so the log says why the checks will report
			// a missing account until one is written there.
			logging.Log.Warn("no account is written down yet, write one through the settings page")
		}
	}

	// A second copy is told where the page is, or that there is none, so its
	// request gets an answer. The address is read when it is asked for: the page
	// is opened with a one-off one that is then replaced.
	go platform.WaitForSettingsRequest(func() {
		if settingsServer == nil {
			logging.Log.Warn("another copy asked for the settings page, which the background mode does not serve")
			return
		}
		// The address is made so the request can be answered with one, but it
		// carries the one time token and is not used here, so it is not logged.
		if _, err := settingsServer.URL(); err != nil {
			logging.Log.Warn("another copy asked for the settings page, and its address could not be made: %v", err)
			return
		}
		logging.Log.Info("another copy asked for the settings page, it was handed the current address")
	})

	runHeadless(state, stop)
	return 0
}

// servesBeyondThisMachine reports whether addr, the address the settings page is
// served on, can be reached from another machine: the token in the address it is
// handed out with is then the only thing protecting a page that writes the
// configuration. An address this program cannot read is answered with yes, since
// what it means is not knowable here.
func servesBeyondThisMachine(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return false
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return !ip.IsLoopback()
	}
	// A name that is not localhost, or no host at all: the second binds every
	// interface, and where the first points is nothing this program can say.
	return true
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
// exit code 1.
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

// updateClient is the release page of this project, as a variable so a test can
// point the command at a forge of its own. The address is the one the program
// already carries, see app.Repo.
var updateClient = app.UpdateClient

// cmdUpdate is the manual check. The reader asked for it here, so it runs
// whatever checkForUpdates says; updateChannel is still honoured, since it
// decides which releases the check may take.
func cmdUpdate(appDir string, opts options) int {
	cfg, ok := loadForCLI(appDir, opts)
	if !ok {
		return 1
	}
	current, err := update.ParseVersion(app.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the version of this build cannot be read: %v\n", err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the program file cannot be found: %v\n", err)
		return 1
	}
	target := update.Native(exe)
	client := updateClient()
	channel := cfg.UpdateChannelValue()
	check, err := client.Check(context.Background(), channel, current, target)
	if err != nil {
		// The one failure here: the question itself could not be asked.
		fmt.Fprintf(os.Stderr, "the release page could not be asked: %v\n", err)
		return 1
	}

	// The check is reported whatever the install did: a reader who asked for one
	// still wants to know what was found, and why nothing happened.
	var installed string
	var installErr error
	if opts.install {
		installed, installErr = installRelease(client, check, target)
	}
	if installErr != nil {
		fmt.Fprintln(os.Stderr, installErr)
	}

	report := newUpdateReport(check, installed)
	if opts.json {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "writing the update report: %v\n", err)
			return 1
		}
	} else {
		printUpdateReport(report)
	}
	if installErr != nil {
		return 1
	}
	return 0
}

// installRelease puts the release the check found in place. It returns the
// program file that was written, and an error when an install was asked for and
// could not be done; having nothing to install is not a failure.
func installRelease(c *update.Client, check update.Check,
	t update.Target) (string, error) {
	switch {
	case !check.Newer:
		return "", nil
	case !check.Signed:
		return "", errors.New("nothing was installed: this build carries no signing" +
			" key, so it cannot check what it downloads")
	case !check.Install.Possible:
		return "", fmt.Errorf("nothing was installed: %s", check.Install.Reason)
	}
	path, err := c.Install(context.Background(), check.Release, t)
	if err != nil {
		return "", fmt.Errorf("nothing was installed: %w", err)
	}
	logging.Log.Info("%s installed over %s, it takes effect on the next start",
		check.Latest, path)
	return path, nil
}

// updateReport is the machine readable form of the update command, in the style
// of statusReport: one line of JSON holding what the text form prints.
type updateReport struct {
	Channel string `json:"channel"`
	Current string `json:"current"`
	// Latest is the release that was found, empty when nothing is newer.
	Latest string `json:"latest"`
	Newer  bool   `json:"newer"`
	// Installable is whether this copy can put the release in place, and Reason
	// says why not when it cannot.
	Installable bool   `json:"installable"`
	Signed      bool   `json:"signed"`
	Reason      string `json:"reason,omitempty"`
	// Detail is the one line a reader is shown.
	Detail string `json:"detail"`
	// Installed is the program file a release was written over, empty when
	// nothing was installed.
	Installed string `json:"installed,omitempty"`
}

// newUpdateReport is the report of one check, and of the install of it when one
// was asked for and done.
func newUpdateReport(c update.Check, installed string) updateReport {
	r := updateReport{
		Channel:     string(c.Channel),
		Current:     c.Current,
		Latest:      c.Latest,
		Newer:       c.Newer,
		Installable: c.Install.Possible,
		Signed:      c.Signed,
		Detail:      c.Describe(),
		Installed:   installed,
	}
	if !c.Install.Possible {
		r.Reason = c.Install.Reason
	}
	if installed != "" {
		r.Detail = c.Latest + " was installed over " + installed +
			", it takes effect on the next start"
	}
	return r
}

// printUpdateReport writes what was found and what was done about it. The lines
// are the fields the JSON form carries, so a reader can compare the two.
func printUpdateReport(r updateReport) {
	fmt.Printf("channel      : %s\n", r.Channel)
	fmt.Printf("current      : %s\n", r.Current)
	fmt.Printf("latest       : %s\n", blank(r.Latest))
	fmt.Printf("newer        : %v\n", r.Newer)
	fmt.Printf("installable  : %v\n", r.Installable)
	fmt.Printf("signed       : %v\n", r.Signed)
	if r.Reason != "" {
		fmt.Printf("reason       : %s\n", r.Reason)
	}
	if r.Installed != "" {
		fmt.Printf("installed    : %s\n", r.Installed)
	}
	fmt.Printf("detail       : %s\n", r.Detail)
}

// The exit codes of the login command. A monitoring script loops on this command
// and reads its code rather than the line it prints, and the two failures are not
// the same thing: sending a password the portal has already refused again is what
// locks an account, so a refusal has to stop a retry loop while an unreachable
// portal must not.
const (
	// The portal accepted the credentials, or the client was already online, so
	// there was nothing to submit.
	loginSignedIn = 0
	// There is nothing to sign in with: no account, or a configuration that
	// cannot be read.
	loginCannotRun = 1
	// The portal answered and refused these credentials: do not retry, check the
	// account and the password first.
	loginRefused = 4
	// The portal could not be asked, or answered something that is not a login
	// answer: retrying later is the right thing to do.
	loginUnreachable = 5
	// Another copy of the program is running and owns the login.
	loginAlreadyRunning = 6
)

// loginExitCode turns one attempt into the code a script reads. An accepted
// login and a session that was already up are the same answer for a retry loop;
// the difference that matters is between a portal that refused the password and a
// portal that could not be asked.
func loginExitCode(test app.LoginTest) int {
	switch {
	case test.AlreadyOnline || test.Result.Success:
		return loginSignedIn
	case test.Result.RetCode != "" || (test.After.Reachable && test.After.Known):
		// The portal answered, with its own ret_code or with a usable view of the
		// session, so the refusal is its verdict on these credentials. A login
		// request that failed while the portal page answered is reported as a
		// refusal too: the answer a script must not retry on is the safe one to
		// give when the two cannot be told apart.
		return loginRefused
	default:
		return loginUnreachable
	}
}

func cmdLogin(appDir string, opts options) int {
	// The portal keeps one session per address, and a copy running in the
	// background is already signing in with the same account: a second request
	// from here would race that copy's check loop over one session. The copy that
	// is running is the one that signs in, so this one stops and says so.
	if !platform.AcquireSingleInstance(false) {
		fmt.Fprintln(os.Stderr, "another copy of the program is running and owns the login; this one submits nothing")
		return loginAlreadyRunning
	}
	cfg, ok := loadForCLI(appDir, opts)
	if !ok {
		return loginCannotRun
	}
	attempts := cfg.Attempts()
	if len(attempts) == 0 {
		fmt.Fprintln(os.Stderr, "no account configured, run: SDAutoLogin config")
		return loginCannotRun
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
		return loginSignedIn
	}
	fmt.Printf("submitted    : %s (password %d chars, entry %d of %d)\n",
		attempt.Entry(), len(attempt.Password), attempt.Index+1, len(cfg.Accounts))
	fmt.Printf("success      : %v\n", test.Result.Success)
	fmt.Printf("ret_code     : %s\n", blank(test.Result.RetCode))
	fmt.Printf("message      : %s\n", test.Result.Message)
	// The reply is the portal's own text, and a portal that answers an error page
	// or an echo of the request can carry the password in it: what is printed is
	// masked the same way the request in the log is.
	fmt.Printf("raw_reply    : %s\n", portal.Truncate(portal.MaskSecrets(test.Result.Raw)))
	fmt.Printf("after        : online=%v ip=%s account=%s\n",
		test.After.Online, blank(test.After.ClientIP), blank(test.After.Account))

	if !test.Result.Success {
		code := loginExitCode(test)
		if code == loginRefused {
			// The code is what a script reads, so the one thing it must not do
			// is said here as well, next to the answer of the portal.
			fmt.Fprintln(os.Stderr, "the portal refused these credentials; do not retry, check the account and the password first")
		}
		return code
	}
	return loginSignedIn
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
	if opts.accountGiven() {
		// The account came from the command line and is already written down, and the
		// wizard would ask for it again: its password prompt cannot offer a stored
		// secret without printing it, so Enter there would drop the password this run
		// just saved.
		return 0
	}
	if err := runSetupWizard(cfg); err != nil {
		// The wizard says what went wrong; the exit status is what a script
		// reads, and a configuration that was not written is not a success.
		return 1
	}
	return 0
}

func blank(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
