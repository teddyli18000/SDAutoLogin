package main

import (
	"encoding/json"
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
loopback for your browser.

Commands:
  run            run in the background, no tray
  status         print the current session state once and exit
  status --json  print the same as one line of JSON, for a monitoring script
  login          submit one login with the configured account and print the reply
  config         interactive setup, writes config.json
  config --show  print the effective configuration (password never included)
  version        print the version
  help           print this text

Global option:
  --home=DIR          keep config.json and logs/ in DIR instead of next to the
                      executable, which is what a service or a container uses to
                      point them at a writable directory

Options, for the commands that read the account:
  --username=NAME     use this account instead of the configured one
  --password=SECRET   use this password instead of the configured one
  --isp=SUFFIX        service suffix such as @zgyd, an empty value for the
                      campus network itself
  --save              write what was given here into config.json as well,
                      instead of using it for this run only

Option for the run command:
  --settings=ADDR     also serve the settings page on ADDR, for example
                      127.0.0.1:8088 or 0.0.0.0:8088, so a machine with no tray
                      and no display can still be configured from a browser

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

// options are the values the command line may override, which is what makes the
// program usable where nobody can answer the setup wizard.
type options struct {
	// home is the directory that holds config.json and logs/, when it is not
	// the one next to the executable.
	home     *string
	username *string
	password *string
	isp      *string
	// settings is the address to serve the settings page on, for the run
	// command; nil leaves the page off.
	settings *string
	// save also writes the overrides to config.json.
	save bool
	// json prints the status report as JSON instead of text.
	json bool
}

// knownOption reports whether arg is one of the options this program defines.
// It is how a forgotten value is told apart from a value that merely starts with
// a dash, such as a password.
func knownOption(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	switch name {
	case "--home", "--username", "--password", "--isp", "--settings", "--save", "--json":
		return true
	}
	return false
}

// takesValue reports whether the option needs a value of its own.
func takesValue(name string) bool {
	switch name {
	case "--home", "--username", "--password", "--isp", "--settings":
		return true
	}
	return false
}

// knownWord reports whether arg is one of the arguments that start with a dash
// without being an option this program reads: the shortcuts, and the flag the
// config command takes. They are left in the argument list for the command to
// pick up, and naming them is what tells a misspelled option apart from them.
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
	case "--isp":
		v := value
		o.isp = &v
	case "--settings":
		v := value
		o.settings = &v
	case "--save":
		o.save = true
	case "--json":
		o.json = true
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
	return o.username != nil || o.password != nil || o.isp != nil
}

// applyTo copies the overrides onto cfg. An option that was not given leaves the
// configured value alone, so one secret can be passed without repeating the rest.
func (o options) applyTo(cfg *config.Config) {
	if o.username != nil {
		cfg.Username = *o.username
	}
	if o.password != nil {
		cfg.Password = *o.password
	}
	if o.isp != nil {
		cfg.AccountSuffix = *o.isp
	}
}

// check reports an option that does not belong to the command that was asked
// for, rather than letting it be silently ignored.
func (o options) check(command string) error {
	switch {
	case o.settings != nil && command != "run" && command != "daemon":
		return fmt.Errorf("--settings belongs to the run command, %s does not serve a settings page", command)
	case o.json && command != "status":
		return fmt.Errorf("--json belongs to the status command, %s has nothing to report as JSON", command)
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
		opts.applyTo(cfg)
		if opts.save {
			// Save drops the password when it is not remembered, and the caller
			// explicitly asked for these values to be written down.
			if opts.password != nil {
				cfg.RememberPassword = true
			}
			if err := cfg.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "writing the config: %v\n", err)
				return cfg, false
			}
			fmt.Fprintf(os.Stderr, "config updated: %s\n", cfg.Path())
		} else {
			fmt.Fprintln(os.Stderr, "note: the values given on the command line are used for this run only, add --save to write them to the config file")
		}
	}

	i18n.SetLang(i18n.Lang(cfg.Language))
	logging.Log.SetVerbose(cfg.VerboseLog)
	return cfg, true
}

func cmdRun(appDir string, opts options) int {
	cfg, ok := loadForCLI(appDir, opts)
	if !ok {
		return 1
	}
	if !cfg.Configured() {
		// A service has nobody to answer the setup wizard, and a config file
		// written from an empty prompt is worse than a clear failure.
		fmt.Fprintln(os.Stderr, "no account configured: pass --username and --password, or run the config command first")
		return 1
	}
	if !platform.AcquireSingleInstance(false) {
		fmt.Fprintln(os.Stderr, "another instance is already running")
		return 1
	}

	state := app.New(cfg)
	stop := make(chan struct{})
	settingsURL := ""

	if opts.settings != nil {
		// The page does not need a tray, so a machine with no display can be
		// configured from a browser the same way a desktop is.
		server := app.NewServer(state, *opts.settings, func() {
			// The page asked the program to exit, which ends a background run
			// the same way a signal does.
			close(stop)
		})
		defer server.Stop()

		url, err := server.URL()
		if err != nil {
			fmt.Fprintf(os.Stderr, "settings page: %v\n", err)
			return 1
		}
		settingsURL = url
	}

	// A second copy is told where the page is, or that there is none, so the
	// request does not go nowhere.
	go platform.WaitForSettingsRequest(func() {
		if settingsURL != "" {
			logging.Log.Info("another copy asked for the settings page, it is at %s", settingsURL)
			return
		}
		logging.Log.Warn("another copy asked for the settings page, which the background mode does not serve")
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

// statusState names what the check found, which is finer grained than the exit
// code: a portal that cannot be reached and one that answers nothing useful are
// both exit code 1, but they are a different story to tell.
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

// statusExitCode is what a monitoring script reads: 0 online, 1 unknown or a
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
	if !cfg.Configured() {
		fmt.Fprintln(os.Stderr, "no account configured, run: SDAutoLogin config")
		return 1
	}

	test := app.New(cfg).TestLogin()
	fmt.Printf("before       : online=%v ip=%s account=%s\n",
		test.Before.Online, blank(test.Before.ClientIP), blank(test.Before.Account))
	fmt.Printf("submitted    : %s (password %d chars)\n",
		cfg.Account(), len(cfg.Password))
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
