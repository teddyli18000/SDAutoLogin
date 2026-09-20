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
	Log.Info("SDAutoLogin starting, app dir %s", appDir)

	cfg, err := LoadConfig(filepath.Join(appDir, "config.json"))
	if err != nil {
		Log.Error("loading config: %v", err)
	}
	Log.SetVerbose(cfg.VerboseLog)
	Log.Debug("config loaded from %s: %s", cfg.Path(), cfg.Summary())

	for _, arg := range os.Args[1:] {
		if arg == "-test-login" {
			// The self test is a diagnostic, so always keep the detail.
			Log.SetVerbose(true)
			testLogin(cfg)
			return
		}
	}

	if !cfg.Configured() {
		Log.Warn("no account configured yet, config file: %s", cfg.Path())
	}

	runUI(NewService(cfg), cfg, appDir)
	Log.Info("SDAutoLogin stopped")
}

// testLogin submits one login request with the configured account and prints
// the portal reply. It is meant for verifying the request format and never
// logs the session out.
func testLogin(cfg *Config) {
	client := NewClient(cfg)
	st := client.QueryStatus()

	fmt.Println("portal reachable :", st.Reachable)
	fmt.Println("state known      :", st.Known)
	fmt.Println("portal online    :", st.Online)
	fmt.Println("client ip        :", st.ClientIP)
	fmt.Println("current account  :", st.Account)
	fmt.Println("detail           :", st.Message)
	if !st.Reachable {
		fmt.Println("portal unreachable, aborting")
		return
	}

	fmt.Println()
	fmt.Println("submitting account :", cfg.Account())
	fmt.Printf("submitting password: %s (%d chars)\n", strings.Repeat("*", len(cfg.Password)), len(cfg.Password))
	fmt.Printf("request            : %s?c=Portal&a=login (login_method=1)\n", cfg.EportalURL)

	res := client.Login(st)
	fmt.Println("success   :", res.Success)
	fmt.Println("ret_code  :", res.RetCode)
	fmt.Println("message   :", res.Message)
	fmt.Println("raw reply :", truncate(res.Raw))

	after := client.QueryStatus()
	fmt.Println()
	fmt.Printf("portal after request: online=%v ip=%s account=%s\n", after.Online, after.ClientIP, after.Account)
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

// runHeadless is the fallback used on platforms without a tray and when the
// tray cannot be created.
func runHeadless(svc *Service, cfg *Config, appDir string) {
	if !cfg.Configured() {
		runSetupWizard(cfg)
	}
	var last State
	svc.SetListener(func(s Snapshot) {
		if s.State != last {
			last = s.State
			Log.Info("status: %s", describeSnapshot(s))
		} else {
			Log.Debug("status: %s", describeSnapshot(s))
		}
	})
	svc.Start()
	Log.Info("running in background, config file %s, press Ctrl+C to quit", cfg.Path())

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	svc.Stop()
	Log.Info("quit signal received")
}
