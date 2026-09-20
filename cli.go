package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The command line stays English on purpose: it is used from servers, from
// scripts and next to the log output, all of which are English as well.

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `SDAutoLogin %s - campus network auto login

Usage:
  SDAutoLogin [command] [flags]

Without a command the tray icon starts and the settings page is served on
loopback for your browser.

Commands:
  run            run in the background, no tray and no settings page
  status         print the current session state once and exit
  login          submit one login with the configured account and print the reply
  config         interactive setup, writes config.json
  config --show  print the effective configuration (password never included)
  version        print the version
  help           print this text

Flags:
  -cli           same as the run command
  -test-login    same as the login command

Files:
  config.json and logs/ live next to the executable.
`, appVersion)
}

func runCommand(args []string, appDir string) int {
	switch args[0] {
	case "run", "daemon":
		return cmdRun(appDir)
	case "status":
		return cmdStatus(appDir)
	case "login":
		return cmdLogin(appDir)
	case "config":
		return cmdConfig(appDir, args[1:])
	case "version":
		fmt.Println(versionLine())
		return 0
	case "help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
}

// loadForCLI reads config.json and applies the configured interface language.
// It reports a hard failure so commands can return a non zero exit code.
func loadForCLI(appDir string) (*Config, bool) {
	cfg, err := LoadConfig(filepath.Join(appDir, "config.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return cfg, false
	}
	SetLang(Lang(cfg.Language))
	Log.SetVerbose(cfg.VerboseLog)
	return cfg, true
}

func cmdRun(appDir string) int {
	cfg, ok := loadForCLI(appDir)
	if !ok {
		return 1
	}
	runHeadless(newAppState(cfg), appDir)
	return 0
}

func cmdStatus(appDir string) int {
	cfg, ok := loadForCLI(appDir)
	if !ok {
		return 1
	}
	st := NewClient(cfg).QueryStatus()

	fmt.Printf("portal       : %s\n", cfg.PortalBaseURL)
	fmt.Printf("reachable    : %v\n", st.Reachable)
	fmt.Printf("state_known  : %v\n", st.Known)
	fmt.Printf("online       : %v\n", st.Online)
	fmt.Printf("client_ip    : %s\n", blank(st.ClientIP))
	fmt.Printf("account      : %s\n", blank(st.Account))
	fmt.Printf("online_since : %s\n", blank(st.OnlineSince))
	fmt.Printf("detail       : %s\n", st.Message)

	if !st.Known {
		return 1
	}
	if !st.Online {
		return 3
	}
	return 0
}

func cmdLogin(appDir string) int {
	cfg, ok := loadForCLI(appDir)
	if !ok {
		return 1
	}
	if !cfg.Configured() {
		fmt.Fprintln(os.Stderr, "no account configured, run: SDAutoLogin config")
		return 1
	}

	test := newAppState(cfg).TestLogin()
	fmt.Printf("before       : online=%v ip=%s account=%s\n",
		test.Before.Online, blank(test.Before.ClientIP), blank(test.Before.Account))
	fmt.Printf("submitted    : %s (password %d chars)\n",
		cfg.Account(), len(cfg.Password))
	fmt.Printf("success      : %v\n", test.Result.Success)
	fmt.Printf("ret_code     : %s\n", blank(test.Result.RetCode))
	fmt.Printf("message      : %s\n", test.Result.Message)
	fmt.Printf("raw_reply    : %s\n", truncate(test.Result.Raw))
	fmt.Printf("after        : online=%v ip=%s account=%s\n",
		test.After.Online, blank(test.After.ClientIP), blank(test.After.Account))

	if !test.Result.Success {
		return 1
	}
	return 0
}

func cmdConfig(appDir string, rest []string) int {
	cfg, ok := loadForCLI(appDir)
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
