package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"sdautologin/internal/app"
	"sdautologin/internal/config"
	"sdautologin/internal/platform"
	"sdautologin/internal/portal"
	"sdautologin/internal/update"
)

func TestParseOptionsAcceptsBothForms(t *testing.T) {
	forms := [][]string{
		{"--username=s1234567", "--password=p@ss", "--isp=@zgyd"},
		{"--username", "s1234567", "--password", "p@ss", "--isp", "@zgyd"},
	}

	for _, args := range forms {
		opts, rest, err := parseOptions(args)
		if err != nil {
			t.Fatalf("parseOptions(%v): %v", args, err)
		}
		if len(rest) != 0 {
			t.Errorf("parseOptions(%v) left %v over", args, rest)
		}
		if opts.username == nil || *opts.username != "s1234567" {
			t.Errorf("parseOptions(%v) username = %v", args, opts.username)
		}
		if opts.password == nil || *opts.password != "p@ss" {
			t.Errorf("parseOptions(%v) password = %v", args, opts.password)
		}
		if opts.isp == nil || *opts.isp != "@zgyd" {
			t.Errorf("parseOptions(%v) isp = %v", args, opts.isp)
		}
		if !opts.given() {
			t.Errorf("parseOptions(%v) reported nothing as given", args)
		}
	}
}

func TestParseOptionsAcceptsThePasswordFileInBothForms(t *testing.T) {
	for _, args := range [][]string{
		{"--password-file=/etc/sdautologin/secret"},
		{"--password-file", "/etc/sdautologin/secret"},
	} {
		opts, rest, err := parseOptions(args)
		if err != nil {
			t.Fatalf("parseOptions(%v): %v", args, err)
		}
		if len(rest) != 0 {
			t.Errorf("parseOptions(%v) left %v over", args, rest)
		}
		if opts.passwordFile == nil || *opts.passwordFile != "/etc/sdautologin/secret" {
			t.Errorf("parseOptions(%v) passwordFile = %v", args, opts.passwordFile)
		}
		if !opts.given() {
			t.Errorf("parseOptions(%v) reported nothing as given", args)
		}
	}
}

func TestParseOptionsLeavesTheOtherArgumentsAlone(t *testing.T) {
	opts, rest, err := parseOptions([]string{"--show", "--username=s1234567", "extra"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}

	if len(rest) != 2 || rest[0] != "--show" || rest[1] != "extra" {
		t.Errorf("rest = %v, want the arguments that are not options", rest)
	}
	if opts.username == nil || *opts.username != "s1234567" {
		t.Errorf("username = %v", opts.username)
	}
}

func TestParseOptionsRejectsAnUnknownOption(t *testing.T) {
	// A misspelled option has to fail, so a monitoring script that writes
	// --jason gets an error and no status report.
	for _, args := range [][]string{
		{"--jason"},
		{"--username=s1234567", "--savee"},
		{"-save"},
		{"status", "--jason"},
		{"--home=/var/lib/sdautologin", "--setings=127.0.0.1:8088"},
	} {
		if _, _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%v) accepted an option it does not define", args)
		}
	}
}

func TestParseOptionsKeepsTheShortcutsAndTheConfigFlag(t *testing.T) {
	// What begins with a dash without being an option belongs to the caller: the
	// shortcuts and the flag of the config command. The program reads those out of
	// what is left, and none of them may be mistaken for a typo.
	for _, args := range [][]string{
		{"-cli"}, {"--cli"}, {"-daemon"}, {"--daemon"}, {"-headless"},
		{"-h"}, {"--help"}, {"-help"}, {"-v"}, {"--version"},
		{"-test-login"}, {"--test-login"},
		{"config", "--show"}, {"config", "-show"},
	} {
		opts, rest, err := parseOptions(args)
		if err != nil {
			t.Errorf("parseOptions(%v): %v", args, err)
			continue
		}
		if len(rest) != len(args) {
			t.Errorf("parseOptions(%v) left %v over, want the arguments handed back", args, rest)
		}
		if !emptyOptions(opts) {
			t.Errorf("parseOptions(%v) read an option out of %v", args, rest)
		}
	}
}

// emptyOptions reports whether the arguments carried no option.
func emptyOptions(o options) bool {
	return o.home == nil && o.username == nil && o.password == nil && o.isp == nil &&
		len(o.accounts) == 0 && o.settings == nil && o.app == nil &&
		!o.save && !o.json && !o.noLog
}

func TestParseOptionsRejectsAMissingValue(t *testing.T) {
	for _, args := range [][]string{{"--password"}, {"--username", "--isp"}} {
		if _, _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%v) accepted an option without a value", args)
		}
	}
}

func TestParseOptionsAcceptsAValueThatLooksLikeADash(t *testing.T) {
	// A password may well start with a dash. Only the option names this program
	// defines are treated as a value that was forgotten.
	opts, _, err := parseOptions([]string{"--password", "--not-an-option"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}

	if opts.password == nil || *opts.password != "--not-an-option" {
		t.Errorf("password = %v", opts.password)
	}
}

func TestParseOptionsTakesTheOptionsThatHaveNoValue(t *testing.T) {
	// --save takes no value, so it is recorded on its own and leaves no stray
	// argument behind.
	opts, rest, err := parseOptions([]string{"--username=u", "--save"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if !opts.save {
		t.Error("--save was not recorded")
	}
	if opts.username == nil || *opts.username != "u" {
		t.Errorf("username = %v", opts.username)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want nothing left over", rest)
	}

	if alone, _, err := parseOptions([]string{"--save"}); err != nil || !alone.save {
		t.Errorf("parseOptions([--save]) = %v, %v", alone, err)
	}
}

func TestParseOptionsSilencesTheLogFile(t *testing.T) {
	// --no-log takes no value, belongs to no command in particular, and a machine
	// with a small flash passes it on every start.
	opts, rest, err := parseOptions([]string{"run", "--home=/etc/sdautologin", "--no-log"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if !opts.noLog {
		t.Error("--no-log was not recorded")
	}
	if len(rest) != 1 || rest[0] != "run" {
		t.Errorf("rest = %v, want the command left alone", rest)
	}
	if !opts.given() {
		t.Error("--no-log is a value that was given, so --save writes it down")
	}
	if opts.accountGiven() {
		t.Error("--no-log is not an account value, so the reminder about --save is not for it")
	}

	cfg := &config.Config{LogToFile: true}
	opts.applyTo(cfg)
	if cfg.LogToFile {
		t.Error("--no-log did not turn the log file off")
	}
}

func TestParseOptionsKeepsAnExplicitlyEmptyValue(t *testing.T) {
	// An empty suffix asks for the campus network itself: it has no operator
	// suffix, and leaving the option out would keep the configured one.
	opts, _, err := parseOptions([]string{"--isp="})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}

	if opts.isp == nil {
		t.Fatal("isp was not recorded")
	}
	if *opts.isp != "" {
		t.Errorf("isp = %q, want an empty value", *opts.isp)
	}
	if !opts.given() {
		t.Error("an explicitly empty value still counts as given")
	}
}

func TestParseOptionsReportsNothingForNoOptions(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", "")
	var opts options
	if opts.given() {
		t.Error("empty options must not count as given")
	}
}

func TestApplyToOnlyChangesWhatWasGiven(t *testing.T) {
	// An option that was not given leaves the entry alone, so one secret can be
	// passed without repeating the rest.
	cfg := &config.Config{Accounts: []config.Account{
		{Username: "old", Password: "old", ISPs: []string{"@cucc"}},
	}}
	password := "new"

	if err := (options{password: &password}).applyTo(cfg); err != nil {
		t.Fatal(err)
	}

	if len(cfg.Accounts) != 1 {
		t.Fatalf("Accounts = %+v, want the one entry", cfg.Accounts)
	}
	entry := cfg.Accounts[0]
	if entry.Password != "new" {
		t.Errorf("Password = %q, want the override", entry.Password)
	}
	if entry.Username != "old" || len(entry.ISPs) != 1 || entry.ISPs[0] != "@cucc" {
		t.Errorf("an option that was not given changed the entry: %+v", entry)
	}
}

// writeSecret leaves a password in a file of its own, the way a service or a
// script hands one over.
func writeSecret(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "portal-password")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A password read from a file is the form a service uses: a process's command line
// is readable by every user that can see the process list, and the configuration
// file is the file the secret is kept out of.
func TestApplyToReadsThePasswordFromAFile(t *testing.T) {
	path := writeSecret(t, "fromthefile\n")
	cfg := &config.Config{Accounts: []config.Account{
		{Username: "s1234567", Password: "stale", ISPs: []string{"@zgyd"}},
	}}

	if err := (options{passwordFile: &path}).applyTo(cfg); err != nil {
		t.Fatalf("applyTo: %v", err)
	}

	entry := cfg.Accounts[0]
	if entry.Password != "fromthefile" {
		t.Errorf("Password = %q, want the one in the file", entry.Password)
	}
	if entry.Username != "s1234567" || len(entry.ISPs) != 1 || entry.ISPs[0] != "@zgyd" {
		t.Errorf("the entry was changed beyond its password: %+v", entry)
	}
}

// systemd hands a LoadCredential secret to the one service that asked for it,
// through a directory no other process on the machine can read, so a unit can
// keep the password out of every file the program writes.
func TestApplyToTakesThePasswordSystemdHandedOver(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, credentialName), []byte("fromsystemd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", dir)

	cfg := &config.Config{Accounts: []config.Account{
		{Username: "s1234567", Password: "stale", ISPs: []string{""}},
	}}
	if err := (options{}).applyTo(cfg); err != nil {
		t.Fatalf("applyTo: %v", err)
	}

	if got := cfg.Accounts[0].Password; got != "fromsystemd" {
		t.Errorf("Password = %q, want the credential", got)
	}
}

func TestARunWithoutACredentialKeepsTheConfiguredPassword(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", t.TempDir())
	cfg := &config.Config{Accounts: []config.Account{
		{Username: "s1234567", Password: "stored", ISPs: []string{""}},
	}}

	if err := (options{}).applyTo(cfg); err != nil {
		t.Fatalf("applyTo: %v", err)
	}
	if got := cfg.Accounts[0].Password; got != "stored" {
		t.Errorf("Password = %q, want the configured one", got)
	}
}

func TestACommandLinePasswordWinsOverACredential(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, credentialName), []byte("fromsystemd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", dir)

	password := "fromthecommandline"
	cfg := &config.Config{Accounts: []config.Account{{Username: "s1234567", ISPs: []string{""}}}}
	if err := (options{password: &password}).applyTo(cfg); err != nil {
		t.Fatalf("applyTo: %v", err)
	}
	if got := cfg.Accounts[0].Password; got != password {
		t.Errorf("Password = %q, want the one that was named", got)
	}
}

// A unit that hands the secret over names no account on its command line, so the
// credential is the only thing this run was given and the options have to be
// applied for it to be read.
func TestARunWithACredentialHasSomethingToApply(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, credentialName), []byte("fromsystemd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", dir)

	var opts options
	if !opts.given() {
		t.Error("the credential was not taken for something to apply")
	}
	if opts.accountGiven() {
		t.Error("the credential is not an account option, so the reminder about --save is not for it")
	}
}

// The whole way in, not applyTo alone: loadForCLI decides whether the options are
// applied, and a real start has to end up with the password the service manager
// handed over.
func TestLoadForCLITakesThePasswordSystemdHandedOver(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, credentialName), []byte("fromsystemd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", dir)

	// The account is written down without a password: the secret is handed over
	// separately.
	appDir := t.TempDir()
	stored, err := config.LoadConfig(filepath.Join(appDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	stored.Accounts = []config.Account{{Username: "s1234567", ISPs: []string{""}}}
	if err := stored.Save(); err != nil {
		t.Fatal(err)
	}

	cfg, ok := loadForCLI(appDir, options{})
	if !ok {
		t.Fatal("a run that was handed a credential was refused")
	}
	if cfg.Accounts[0].Password != "fromsystemd" {
		t.Errorf("Password = %q, want the credential", cfg.Accounts[0].Password)
	}
	if !cfg.Configured() {
		t.Error("the run was left with nothing to sign in with")
	}
}

// An account named without a service type is the campus network and has to be
// usable as it stands: a run given this is not saved, so it never passes through
// the normalization that fills the service type in.
func TestAnAccountWithoutAServiceTypeCanSignIn(t *testing.T) {
	appDir := t.TempDir()
	cfg, ok := loadForCLI(appDir, options{accounts: []string{"s1234567:topsecret"}})
	if !ok {
		t.Fatal("the run was refused")
	}
	if !cfg.Configured() {
		t.Fatalf("the run has nothing to sign in with: %+v", cfg.Accounts)
	}
	attempts := cfg.Attempts()
	if len(attempts) != 1 {
		t.Fatalf("Attempts = %+v, want the one entry", attempts)
	}
	if attempts[0].Username != "s1234567" || attempts[0].ISP != "" {
		t.Errorf("the attempt is %+v, want the campus network", attempts[0])
	}
}

// A password on its own says nothing about the rest of the list, so the entries
// after the first are left where they are.
func TestAPasswordAloneLeavesTheRestOfTheListAlone(t *testing.T) {
	password := "onlythis"
	cfg := &config.Config{Accounts: []config.Account{
		{Username: "first", Password: "one", ISPs: []string{""}},
		{Username: "second", Password: "two", ISPs: []string{"@cucc"}},
	}}

	if err := (options{password: &password}).applyTo(cfg); err != nil {
		t.Fatalf("applyTo: %v", err)
	}
	if len(cfg.Accounts) != 2 {
		t.Fatalf("Accounts = %+v, want the list to be left as it was", cfg.Accounts)
	}
	if cfg.Accounts[0].Username != "first" || cfg.Accounts[0].Password != password {
		t.Errorf("the first entry = %+v, want it with the password that was named", cfg.Accounts[0])
	}
	if cfg.Accounts[1].Password != "two" {
		t.Errorf("the second entry = %+v, want it untouched", cfg.Accounts[1])
	}
}

// --save writes down what was named. What a password file names is the path:
// the secret is what that file was chosen to keep out of the configuration, and
// copying it in would defeat the point of naming one.
func TestSavingAPasswordFileRemembersThePathAndNotTheSecret(t *testing.T) {
	secret := writeSecret(t, "fromthefile\n")

	appDir := t.TempDir()
	path := filepath.Join(appDir, "config.json")
	stored, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	stored.Accounts = []config.Account{{Username: "s1234567", ISPs: []string{""}}}
	stored.RememberPassword = false
	if err := stored.Save(); err != nil {
		t.Fatal(err)
	}

	if _, ok := loadForCLI(appDir, options{passwordFile: &secret, save: true}); !ok {
		t.Fatal("the run was refused")
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "fromthefile") {
		t.Errorf("the secret was written into the configuration file:\n%s", written)
	}
	// What the file records is read back rather than searched for as text: on
	// Windows the path is a name like C:\Users\... and JSON writes each
	// backslash twice, so a search for the path as it stands on disk would
	// report it missing while it is there.
	var recorded struct {
		PasswordFile string `json:"passwordFile"`
	}
	if err := json.Unmarshal(written, &recorded); err != nil {
		t.Fatalf("the configuration file must stay valid JSON: %v", err)
	}
	if recorded.PasswordFile != secret {
		t.Errorf("the password file recorded is %q, want %q:\n%s", recorded.PasswordFile, secret, written)
	}

	reread, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reread.RememberPassword {
		t.Error("the password was not written down after --save")
	}
	if len(reread.Accounts) != 1 || reread.Accounts[0].Password != "fromthefile" {
		t.Errorf("Accounts = %+v, want the password read back out of the file", reread.Accounts)
	}
	if got := reread.Accounts[0].PasswordSource; got != config.SourceFile {
		t.Errorf("PasswordSource = %q, want %q", got, config.SourceFile)
	}
	if !reread.Configured() {
		t.Error("the saved configuration has nothing to sign in with")
	}
}

func TestTwoPasswordsAreRefused(t *testing.T) {
	path := writeSecret(t, "fromthefile\n")
	password := "fromthecommandline"
	cfg := &config.Config{Accounts: []config.Account{
		{Username: "s1234567", Password: "stale", ISPs: []string{""}},
	}}

	if err := (options{password: &password, passwordFile: &path}).applyTo(cfg); err == nil {
		t.Error("two sources for one password were accepted")
	}
	if got := cfg.Accounts[0].Password; got != "stale" {
		t.Errorf("a run that was refused changed the entry: %q", got)
	}
}

// --account carries the password in the entry itself, so a password read from a
// file has nowhere to go and is refused.
func TestAnAccountWithAFilePasswordIsRefused(t *testing.T) {
	path := writeSecret(t, "fromthefile\n")
	cfg := &config.Config{}

	if err := (options{accounts: []string{"first:one"}, passwordFile: &path}).applyTo(cfg); err == nil {
		t.Error("--account was combined with a password from a file")
	}
}

func TestApplyToSetsEverythingThatWasGiven(t *testing.T) {
	cfg := &config.Config{AccountPrefix: ",0,"}
	username, password, isp := "s1234567", "p@ss", "@zgyd"

	if err := (options{username: &username, password: &password, isp: &isp}).applyTo(cfg); err != nil {
		t.Fatal(err)
	}

	if len(cfg.Accounts) != 1 {
		t.Fatalf("Accounts = %+v, want a single entry", cfg.Accounts)
	}
	entry := cfg.Accounts[0]
	if entry.Username != username || entry.Password != password {
		t.Errorf("entry = %+v", entry)
	}
	if len(entry.ISPs) != 1 || entry.ISPs[0] != isp {
		t.Errorf("ISPs = %v, want the service that was given", entry.ISPs)
	}
	if got := cfg.Account(username, isp); got != ",0,s1234567@zgyd" {
		t.Errorf("Account() = %q, want the overridden account", got)
	}
}

func TestApplyToReplacesTheListWithTheAccountsGiven(t *testing.T) {
	// A provisioning script names the accounts to use, in the order it names them,
	// and that list replaces whatever the file held.
	cfg := &config.Config{Accounts: []config.Account{
		{Username: "stale", Password: "stale", ISPs: []string{""}},
	}}
	opts := options{accounts: []string{"first:one@zgyd", "second:two@cucc+zgyd", "third:three"}}

	if err := opts.applyTo(cfg); err != nil {
		t.Fatal(err)
	}

	if len(cfg.Accounts) != 3 {
		t.Fatalf("Accounts = %+v, want the three that were given", cfg.Accounts)
	}
	if cfg.Accounts[0].Username != "first" || cfg.Accounts[0].Password != "one" {
		t.Errorf("the first entry is %+v", cfg.Accounts[0])
	}
	if got := cfg.Accounts[0].ISPs; len(got) != 1 || got[0] != "@zgyd" {
		t.Errorf("ISPs = %v, want the one service type", got)
	}
	// The order of the service types is the order they are tried in, so the
	// order they were written in has to survive.
	if got := cfg.Accounts[1].ISPs; len(got) != 2 || got[0] != "@cucc" || got[1] != "@zgyd" {
		t.Errorf("ISPs = %v, want both service types in the order they were given", got)
	}
	// No service type is the campus network, and the suffix is filled in here: a
	// run without --save skips the normalization that would supply it.
	if got := cfg.Accounts[2].ISPs; len(got) != 1 || got[0] != "" {
		t.Errorf("ISPs = %v, want the campus network for the entry that named no service", got)
	}
}

func TestParseAccount(t *testing.T) {
	cases := []struct {
		raw      string
		username string
		password string
		isps     []string
	}{
		// Naming no service is the campus network itself, written as an empty
		// suffix. An entry with no suffix cannot be submitted.
		{"s1234567:simple", "s1234567", "simple", []string{""}},
		{"s1234567:simple@zgyd", "s1234567", "simple", []string{"@zgyd"}},
		{"s1234567:simple@zgyd+cucc", "s1234567", "simple", []string{"@zgyd", "@cucc"}},
		// An empty element in front is the campus network itself, the way to name
		// it when another service type is to be tried after it.
		{"s1234567:simple@+zgyd", "s1234567", "simple", []string{"", "@zgyd"}},
		{"s1234567:@zgyd", "s1234567", "", []string{"@zgyd"}},
		{"s1234567:p:with:colons@zgyd", "s1234567", "p:with:colons", []string{"@zgyd"}},
		{"  s1234567  :simple", "s1234567", "simple", []string{""}},
	}
	for _, c := range cases {
		entry, err := parseAccount(c.raw)
		if err != nil {
			t.Errorf("parseAccount(%q): %v", c.raw, err)
			continue
		}
		if entry.Username != c.username || entry.Password != c.password {
			t.Errorf("parseAccount(%q) = %q/%q, want %q/%q",
				c.raw, entry.Username, entry.Password, c.username, c.password)
		}
		if len(entry.ISPs) != len(c.isps) {
			t.Errorf("parseAccount(%q) ISPs = %v, want %v", c.raw, entry.ISPs, c.isps)
			continue
		}
		for i := range c.isps {
			if entry.ISPs[i] != c.isps[i] {
				t.Errorf("parseAccount(%q) ISPs = %v, want %v", c.raw, entry.ISPs, c.isps)
			}
		}
	}

	// A value that names no account is refused, and so is one with two @ in it:
	// the second @ cannot be told apart from a password that holds one. The
	// message never repeats the value, since it holds the password.
	for _, bad := range []string{
		"", "s1234567", ":hunter2", "   :hunter2",
		"s1234567:hunter2@zgyd@cucc", "s1234567:@zgyd@cucc",
	} {
		_, err := parseAccount(bad)
		if err == nil {
			t.Errorf("parseAccount(%q) accepted a value it cannot read", bad)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("the error repeats the value it refused: %v", err)
		}
	}
}

func TestAccountCannotBeCombinedWithTheSingleAccountOptions(t *testing.T) {
	// The two say the same thing in different ways, and quietly preferring one
	// of them would leave a script with a configuration it did not ask for.
	username := "s1234567"
	cfg := &config.Config{}

	err := options{accounts: []string{"first:one"}, username: &username}.applyTo(cfg)

	if err == nil {
		t.Fatal("--account and --username were accepted together")
	}
	if len(cfg.Accounts) != 0 {
		t.Errorf("the configuration was changed anyway: %+v", cfg.Accounts)
	}
}

func TestParseOptionsTakesTheAccountOptionMoreThanOnce(t *testing.T) {
	opts, rest, err := parseOptions([]string{"--account=first:one", "--account=second:two"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if len(opts.accounts) != 2 || opts.accounts[0] != "first:one" || opts.accounts[1] != "second:two" {
		t.Errorf("accounts = %v, want both of them in order", opts.accounts)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v", rest)
	}
	if !opts.accountGiven() {
		t.Error("--account counts as an account option")
	}
}

func TestParseOptionsAcceptsAHomeDirectory(t *testing.T) {
	// A service or a container keeps its settings outside the directory the
	// executable was dropped in, so --home is read like any other option.
	for _, args := range [][]string{
		{"--home=/var/lib/sdautologin"},
		{"--home", "/var/lib/sdautologin"},
	} {
		opts, rest, err := parseOptions(args)
		if err != nil {
			t.Fatalf("parseOptions(%v): %v", args, err)
		}
		if len(rest) != 0 {
			t.Errorf("parseOptions(%v) left %v over", args, rest)
		}
		if opts.home == nil || *opts.home != "/var/lib/sdautologin" {
			t.Errorf("parseOptions(%v) home = %v", args, opts.home)
		}
	}
}

func TestResolveAppDirLetsHomeWin(t *testing.T) {
	dir := "/var/lib/sdautologin"
	if got := resolveAppDir(&dir); got != dir {
		t.Errorf("resolveAppDir = %q, want %q", got, dir)
	}

	// No --home, or an empty one, leaves the choice to the installation directory.
	// A portable copy relies on that.
	for _, home := range []*string{nil, new(string)} {
		switch got := resolveAppDir(home); got {
		case "":
			t.Errorf("resolveAppDir(%v) returned nothing", home)
		case dir:
			t.Errorf("resolveAppDir(%v) returned %q", home, got)
		}
	}
}

func TestParseOptionsTakesTheAppAddressWhenThereIsOne(t *testing.T) {
	// --app is the one option whose value is optional: the bare form is the
	// common one on a desktop, and an address is only named by a machine that
	// should serve the window somewhere specific.
	bare, rest, err := parseOptions([]string{"--app"})
	if err != nil {
		t.Fatalf("parseOptions(--app): %v", err)
	}
	if bare.app == nil {
		t.Fatal("--app was not recorded")
	}
	if *bare.app != "" {
		t.Errorf("app = %q, want an empty address", *bare.app)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want nothing left over", rest)
	}

	named, _, err := parseOptions([]string{"--app=0.0.0.0:8088"})
	if err != nil {
		t.Fatalf("parseOptions(--app=0.0.0.0:8088): %v", err)
	}
	if named.app == nil || *named.app != "0.0.0.0:8088" {
		t.Errorf("app = %v, want the address that was given", named.app)
	}

	// The spaced form belongs to the options that need a value, and --app is not
	// one of them, so the address is left over as a stray argument and reported.
	spaced, rest, err := parseOptions([]string{"--app", "0.0.0.0:8088"})
	if err != nil {
		t.Fatalf("parseOptions(--app 0.0.0.0:8088): %v", err)
	}
	if spaced.app == nil || *spaced.app != "" {
		t.Errorf("app = %v, want an empty address", spaced.app)
	}
	if len(rest) != 1 || rest[0] != "0.0.0.0:8088" {
		t.Errorf("rest = %v, want the address left over", rest)
	}
}

func TestCheckOnlyAcceptsAnOptionInTheModeItBelongsTo(t *testing.T) {
	addr := "127.0.0.1:8088"

	refused := []struct {
		what    string
		opts    options
		command string
	}{
		{"--settings where no page is served", options{settings: &addr}, "status"},
		{"--json where there is nothing to report", options{json: true}, "run"},
		{"--app where no window is opened", options{app: &addr}, "run"},
		{"--app where no window is opened", options{app: &addr}, "status"},
	}
	for _, c := range refused {
		if err := c.opts.check(c.command); err == nil {
			t.Errorf("%s: check(%s) accepted it", c.what, c.command)
		}
	}

	allowed := []struct {
		opts    options
		command string
	}{
		{options{settings: &addr}, "run"},
		{options{settings: &addr}, "daemon"},
		{options{json: true}, "status"},
		{options{}, "run"},
		// Every command may be told to keep the log on the console alone.
		{options{noLog: true}, "run"},
		{options{noLog: true}, "status"},
		// Asking what the program does is always answered, whatever else was
		// written next to it.
		{options{app: &addr}, "help"},
		{options{json: true}, "version"},
	}
	for _, c := range allowed {
		if err := c.opts.check(c.command); err != nil {
			t.Errorf("check(%s) = %v, want it accepted", c.command, err)
		}
	}
}

// TestTheAutostartEntryRepeatsOnlyTheOptionThatSaysWhereTheSettingsLive covers
// what an entry is allowed to carry. An entry outlives the run, in a place
// anyone can read, so the only thing repeated is --home.
func TestTheAutostartEntryRepeatsOnlyTheOptionThatSaysWhereTheSettingsLive(t *testing.T) {
	account, secret := "23D001", "hunter2"
	addr := "127.0.0.1:8088"

	// A run that was not told where the settings live has nothing to repeat: the
	// entry then starts the program as it was started.
	if got := autostartArguments(options{
		username: &account,
		password: &secret,
		accounts: []string{"23D001:hunter2@zgyd"},
		settings: &addr,
		app:      &addr,
		json:     true,
	}); len(got) != 0 {
		t.Errorf("autostartArguments() with no --home = %q, want nothing", got)
	}

	// A relative directory is made absolute, since an entry does not run in the
	// directory the option was written in.
	rel := filepath.Join("sub", "settings")
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	got := autostartArguments(options{
		home:     &rel,
		username: &account,
		password: &secret,
		accounts: []string{"23D001:hunter2@zgyd"},
		settings: &addr,
		app:      &addr,
		json:     true,
		save:     true,
	})
	if len(got) != 1 {
		t.Fatalf("autostartArguments() = %q, want the one --home argument", got)
	}
	if want := "--home=" + abs; got[0] != want {
		t.Errorf("autostartArguments() = %q, want %q", got[0], want)
	}
	for _, arg := range got {
		if strings.Contains(arg, secret) || strings.Contains(arg, account) {
			t.Errorf("the entry would carry %q, and neither a secret nor an account belongs in a place that outlives the run", arg)
		}
	}

	// An empty --home is the same as none: it names no directory.
	empty := ""
	if got := autostartArguments(options{home: &empty}); got != nil {
		t.Errorf("autostartArguments(--home=) = %q, want nothing", got)
	}
}

// TestTheTrayRefusesAnOptionItCannotHonour covers the mode that names no command.
// --json and --settings belong to the status and run commands, and a tray run that
// quietly dropped them would leave a script waiting for a report that never comes.
func TestTheTrayRefusesAnOptionItCannotHonour(t *testing.T) {
	addr := "127.0.0.1:8088"
	dir := t.TempDir()

	for _, c := range []struct {
		option string
		opts   options
	}{
		{"--json", options{json: true}},
		{"--settings", options{settings: &addr}},
	} {
		err := c.opts.checkTrayOptions()
		if err == nil {
			t.Errorf("%s was accepted by the tray, which has nowhere to put it", c.option)
			continue
		}
		if !strings.Contains(err.Error(), strings.TrimPrefix(c.option, "--")) {
			t.Errorf("the refusal does not name %s: %v", c.option, err)
		}
	}

	// These are the options the tray honours: the ones it was started with, and
	// the two that describe the program itself.
	for _, c := range []options{
		{},
		{home: &dir},
		{noLog: true},
		{app: &addr},
		{home: &dir, noLog: true, app: &addr},
	} {
		if err := c.checkTrayOptions(); err != nil {
			t.Errorf("checkTrayOptions(%+v) = %v, want the tray's own options accepted", c, err)
		}
	}
}

func TestARunWithNothingToSignInWithNeedsTheSettingsPage(t *testing.T) {
	addr := "127.0.0.1:8088"
	empty := &config.Config{}

	// A service has nobody to answer the wizard, so a run that could only report
	// the same failure is refused.
	if err := checkAccountForRun(empty, options{}); err == nil {
		t.Error("a run with no account and no settings page was accepted")
	}

	// With the page served it is started: that page is the way in on a machine whose
	// operator has no other one.
	if err := checkAccountForRun(empty, options{settings: &addr}); err != nil {
		t.Errorf("a run serving the settings page was refused: %v", err)
	}

	// Anything to sign in with is enough, page or no page.
	configured := &config.Config{Accounts: []config.Account{
		{Username: "s1234567", Password: "p@ss", ISPs: []string{""}},
	}}
	if err := checkAccountForRun(configured, options{}); err != nil {
		t.Errorf("a configured run was refused: %v", err)
	}
}

func TestCheckTrayOnlyAllowsAppWhereThereIsATray(t *testing.T) {
	addr := "127.0.0.1:8088"

	// The tray opens a window of its own, so a build that has none refuses the
	// option instead of starting up and ignoring it.
	if err := checkTray(options{app: &addr}, false); err == nil {
		t.Error("--app was accepted in a build without a tray")
	}

	if err := checkTray(options{app: &addr}, true); err != nil {
		t.Errorf("checkTray in a build with a tray = %v, want it accepted", err)
	}

	if err := checkTray(options{}, false); err != nil {
		t.Errorf("checkTray without --app = %v, want it accepted", err)
	}
}

func TestStatusNamesWhatTheCheckFound(t *testing.T) {
	cases := []struct {
		what  string
		st    portal.Status
		state string
		code  int
	}{
		{
			"the portal says the session is up",
			portal.Status{Reachable: true, Known: true, Online: true},
			stateOnline, 0,
		},
		{
			"the portal says the client is not signed in",
			portal.Status{Reachable: true, Known: true},
			stateOffline, 3,
		},
		{
			"the portal answered nothing usable",
			portal.Status{Reachable: true},
			stateUnknown, 1,
		},
		{
			"the portal did not answer at all",
			portal.Status{},
			stateUnreachable, 1,
		},
	}

	for _, c := range cases {
		if got := statusState(c.st); got != c.state {
			t.Errorf("%s: state = %q, want %q", c.what, got, c.state)
		}
		if got := statusExitCode(c.st); got != c.code {
			t.Errorf("%s: exit code = %d, want %d", c.what, got, c.code)
		}
	}
}

func TestStatusJSONCarriesTheFieldsAScriptNeeds(t *testing.T) {
	st := portal.Status{
		Reachable:   true,
		Known:       true,
		Online:      true,
		ClientIP:    "203.0.113.7",
		Account:     "s1234567",
		OnlineSince: "2026-01-01 00:00:00",
		Message:     "online",
	}

	data, err := json.Marshal(newStatusReport("http://10.9.1.3/", st))
	if err != nil {
		t.Fatalf("the report is not encodable: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("the report is not JSON: %v", err)
	}

	// The field names are a promise to any monitoring script, so the whole set is
	// pinned down here.
	want := map[string]any{
		"portal":      "http://10.9.1.3/",
		"state":       "online",
		"reachable":   true,
		"known":       true,
		"online":      true,
		"clientIp":    "203.0.113.7",
		"account":     "s1234567",
		"onlineSince": "2026-01-01 00:00:00",
		"detail":      "online",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %v, want %v", key, got[key], value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the report has %d fields, want %d: %v", len(got), len(want), got)
	}
}

func TestStatusJSONOfAnEmptyAnswerStaysEmpty(t *testing.T) {
	// A missing value must not be turned into the dash the text form uses, since
	// a script would read that as a client address.
	data, err := json.Marshal(newStatusReport("http://10.9.1.3/", portal.Status{}))
	if err != nil {
		t.Fatalf("the report is not encodable: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("the report is not JSON: %v", err)
	}

	if got["state"] != stateUnreachable {
		t.Errorf("state = %v, want %q", got["state"], stateUnreachable)
	}
	for _, key := range []string{"clientIp", "account", "onlineSince", "detail"} {
		if got[key] != "" {
			t.Errorf("%s = %v, want an empty string", key, got[key])
		}
	}
}

// --quiet is for a service whose console goes into the system log, where the
// detail of a check every fifteen seconds is most of what the log ring holds.
// It turns the verbose setting off without touching the file, so a unit can pass
// it on every start the way it passes --no-log.
func TestQuietTurnsOffThePerCheckDetail(t *testing.T) {
	opts, rest, err := parseOptions([]string{"--quiet"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if len(rest) != 0 {
		t.Errorf("parseOptions left %v over", rest)
	}
	if !opts.given() {
		t.Error("a run given --quiet reported nothing to apply")
	}

	cfg := &config.Config{VerboseLog: true, LogToFile: true}
	if err := opts.applyTo(cfg); err != nil {
		t.Fatalf("applyTo: %v", err)
	}
	if cfg.VerboseLog {
		t.Error("--quiet left the per check detail on")
	}
	// Two separate switches: a router passes both, and one must not imply the
	// other.
	if !cfg.LogToFile {
		t.Error("--quiet turned the log file off as well")
	}
}

func TestParseOptionsRecordsTheInstallFlag(t *testing.T) {
	// --install takes no value and belongs to the update command, which is the
	// only one that has anything to put in place.
	opts, rest, err := parseOptions([]string{"update", "--install"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if !opts.install {
		t.Error("--install was not recorded")
	}
	if len(rest) != 1 || rest[0] != "update" {
		t.Errorf("rest = %v, want the command left alone", rest)
	}
	if opts.given() {
		t.Error("--install is not a value --save writes down")
	}
}

func TestInstallAndJSONBelongToTheirCommands(t *testing.T) {
	cases := []struct {
		name    string
		opts    options
		command string
		refused bool
	}{
		{"--install with update", options{install: true}, "update", false},
		{"--install with status", options{install: true}, "status", true},
		{"--install with config", options{install: true}, "config", true},
		{"--json with update", options{json: true}, "update", false},
		{"--json with status", options{json: true}, "status", false},
		{"--json with login", options{json: true}, "login", true},
	}
	for _, c := range cases {
		err := c.opts.check(c.command)
		if (err != nil) != c.refused {
			t.Errorf("%s: check = %v, want it refused: %v", c.name, err, c.refused)
		}
	}

	// The tray names no command, so it has to refuse both, the way it refuses
	// --settings: a run that dropped them would leave a script waiting.
	if err := (options{install: true}).checkTrayOptions(); err == nil {
		t.Error("the tray accepted --install")
	}
	if err := (options{json: true}).checkTrayOptions(); err == nil {
		t.Error("the tray accepted --json")
	}
}

// fakeForge is a release page the tests drive. It answers the releases endpoint
// with the tags it was given and counts the requests, so a test can assert that
// the command did the asking.
type fakeForge struct {
	mu       sync.Mutex
	requests int
}

func (f *fakeForge) client(t *testing.T, releases string) *update.Client {
	t.Helper()
	answer := func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests++
		f.mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, "/releases") {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, releases)
	}
	srv := httptest.NewServer(http.HandlerFunc(answer))
	t.Cleanup(srv.Close)
	return update.NewClient(srv.URL, "zqlan/SDAutoLogin")
}

// asked is how many times the releases were requested.
func (f *fakeForge) asked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// releaseEntry is one release of the releases reply, in the shape the forge API
// answers with.
func releaseEntry(tag string, prerelease bool) string {
	return fmt.Sprintf(`{"tag_name":%q,"prerelease":%v,"draft":false,`+
		`"published_at":"2026-01-01T00:00:00Z","assets":[]}`, tag, prerelease)
}

// releasesAhead is a stable release and a pre-release, both numbered ahead of
// anything this tree carries, so the tests say the same thing whatever version
// the sources hold.
func releasesAhead() string {
	return "[" + releaseEntry("v2.0.0", false) + "," +
		releaseEntry("v2.1.0-beta.1", true) + "]"
}

// useForge points the command at a release page of its own.
func useForge(t *testing.T, client *update.Client) {
	t.Helper()
	previous := updateClient
	updateClient = func() *update.Client { return client }
	t.Cleanup(func() { updateClient = previous })
}

// updateConfigFile writes the configuration the update command reads. The check
// is off, which is the state the manual command has to work in anyway, and the
// channel is the one the test names.
func updateConfigFile(t *testing.T, channel string, checkForUpdates bool) string {
	t.Helper()
	dir := t.TempDir()
	body := fmt.Sprintf(`{"checkForUpdates":%v,"updateChannel":%q}`,
		checkForUpdates, channel)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// captureOutput runs f with stdout and stderr pointing at files, and returns
// what each was written. The commands print with fmt.Print and write their
// errors with fmt.Fprint, both of which read the files when they are called, so
// a swap here catches the output of a command run in a test.
func captureOutput(t *testing.T, f func()) (stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	outPath := filepath.Join(dir, "stdout")
	errPath := filepath.Join(dir, "stderr")

	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	errOut, err := os.Create(errPath)
	if err != nil {
		t.Fatal(err)
	}
	defer errOut.Close()

	previousOut, previousErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, errOut
	defer func() { os.Stdout, os.Stderr = previousOut, previousErr }()

	f()

	return readOutput(t, outPath), readOutput(t, errPath)
}

func readOutput(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// reportOf reads the line of JSON the update command prints. Every field the
// text form carries travels in it, so a script and a reader see one report.
func reportOf(t *testing.T, stdout string) updateReport {
	t.Helper()
	var report updateReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("the report is not one line of JSON: %v, %q", err, stdout)
	}
	return report
}

func TestUpdateCommandReportsANewerReleaseWithTheCheckOff(t *testing.T) {
	// The command is the reader asking, so checkForUpdates does not gate it. The
	// configuration the test writes has the switch off, and the forge is still
	// asked.
	forge := &fakeForge{}
	useForge(t, forge.client(t, releasesAhead()))
	dir := updateConfigFile(t, "stable", false)

	stdout, _ := captureOutput(t, func() {
		if code := runCommand([]string{"update"}, dir, options{}); code != 0 {
			t.Errorf("update = %d, want 0 for a check that ran", code)
		}
	})

	if got := forge.asked(); got != 1 {
		t.Errorf("the release page was asked %d times, want once", got)
	}
	for _, want := range []string{
		"channel      : stable",
		"current      : " + app.Version,
		// The tag as the release page names it, v and all.
		"latest       : v2.0.0",
		"newer        : true",
		"detail       : ",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the report does not hold %q:\n%s", want, stdout)
		}
	}
}

func TestUpdateCommandPrintsTheSameAsOneLineOfJSON(t *testing.T) {
	forge := &fakeForge{}
	useForge(t, forge.client(t, releasesAhead()))
	dir := updateConfigFile(t, "stable", false)

	stdout, _ := captureOutput(t, func() {
		if code := cmdUpdate(dir, options{json: true}); code != 0 {
			t.Errorf("update --json = %d, want 0", code)
		}
	})

	report := reportOf(t, strings.TrimSpace(stdout))
	if report.Latest != "v2.0.0" || !report.Newer || report.Channel != "stable" {
		t.Errorf("report = %+v, want the release it found on the stable channel", report)
	}
	// The line a reader is shown comes from the check, so the report has to carry
	// one; which line it is depends on what this build can check.
	if report.Detail == "" {
		t.Errorf("report = %+v, want the line a reader is shown", report)
	}
	// Whether this build can check what it downloads is a fact about the build,
	// and the report has to carry it as the check reports it.
	if report.Signed != (update.PublicKey != "") {
		t.Errorf("Signed = %v, want the state of the key this build carries", report.Signed)
	}
}

func TestUpdateCommandHonoursTheChannel(t *testing.T) {
	// The newest release is a pre-release, which the stable channel passes over.
	forge := &fakeForge{}
	useForge(t, forge.client(t, releasesAhead()))

	cases := []struct {
		setting string
		want    string
	}{
		{"stable", "v2.0.0"},
		{"prerelease", "v2.1.0-beta.1"},
		// A name this program does not know reads as stable, so a file written by
		// hand cannot widen what the reader is offered.
		{"beta", "v2.0.0"},
		{"", "v2.0.0"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("updateChannel %q", c.setting), func(t *testing.T) {
			dir := updateConfigFile(t, c.setting, false)

			stdout, _ := captureOutput(t, func() {
				if code := cmdUpdate(dir, options{json: true}); code != 0 {
					t.Errorf("update = %d, want 0", code)
				}
			})

			report := reportOf(t, strings.TrimSpace(stdout))
			if report.Latest != c.want {
				t.Errorf("Latest = %q, want %q on channel %q", report.Latest, c.want, c.setting)
			}
		})
	}
}

func TestUpdateCommandReportsBeingUpToDate(t *testing.T) {
	// The releases a program that is current is offered, all older than this
	// build.
	forge := &fakeForge{}
	useForge(t, forge.client(t, "["+releaseEntry("v0.1.0", false)+"]"))
	dir := updateConfigFile(t, "stable", false)

	stdout, _ := captureOutput(t, func() {
		if code := cmdUpdate(dir, options{json: true}); code != 0 {
			t.Errorf("update = %d, want 0 while the check ran", code)
		}
	})

	report := reportOf(t, strings.TrimSpace(stdout))
	if report.Newer || report.Latest != "" {
		t.Errorf("report = %+v, want nothing newer than this build", report)
	}
	if !strings.Contains(report.Detail, "is the newest release") {
		t.Errorf("Detail = %q, want the line that says this build is current", report.Detail)
	}
}

func TestUpdateInstallSaysWhenTheBuildCarriesNoSigningKey(t *testing.T) {
	if update.PublicKey != "" {
		t.Skip("this build carries a signing key, so the missing key is the other case")
	}
	forge := &fakeForge{}
	useForge(t, forge.client(t, releasesAhead()))
	dir := updateConfigFile(t, "stable", false)

	stdout, stderr := captureOutput(t, func() {
		if code := cmdUpdate(dir, options{json: true, install: true}); code != 1 {
			t.Errorf("update --install = %d, want 1 while nothing can be installed", code)
		}
	})

	report := reportOf(t, strings.TrimSpace(stdout))
	if report.Installed != "" {
		t.Errorf("installed %q, want nothing written without a key", report.Installed)
	}
	if !strings.Contains(stderr, "no signing key") {
		t.Errorf("stderr = %q, want the missing key named", stderr)
	}
	// The check itself is still reported: a reader who asked for an install
	// wants to know what was found as well as why nothing happened.
	if report.Latest != "v2.0.0" || !report.Newer {
		t.Errorf("report = %+v, want the release that was found", report)
	}
}

func TestUpdateCommandReportsAReleasePageItCannotAsk(t *testing.T) {
	// A forge that has been shut down again is the release page a machine with no
	// route to the project sees.
	closed := func(http.ResponseWriter, *http.Request) {}
	srv := httptest.NewServer(http.HandlerFunc(closed))
	client := update.NewClient(srv.URL, "zqlan/SDAutoLogin")
	srv.Close()
	useForge(t, client)
	dir := updateConfigFile(t, "stable", false)

	stdout, stderr := captureOutput(t, func() {
		if code := cmdUpdate(dir, options{}); code != 1 {
			t.Errorf("update = %d, want 1 when the page cannot be asked", code)
		}
	})

	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout = %q, want nothing reported when there was no answer", stdout)
	}
	if !strings.Contains(stderr, "release page could not be asked") {
		t.Errorf("stderr = %q, want the reason the check could not be made", stderr)
	}
}

// ---------------------------------------------------------------------------
// Running the program itself
//
// The tests above hand the functions under test slices of their own. The ones
// below start the program the way a user does, through os.Args and main, so what
// is asserted is the exit code, the text and the log file a script and a reader
// get rather than the shape of a call.

// mainChildEnv turns this test binary into the program itself: the value holds
// the arguments of one run, separated by argSep. An environment value cannot
// hold a NUL byte, so a control character no argument here spells is what divides
// them.
const (
	mainChildEnv = "SDAUTOLOGIN_TEST_MAIN"
	argSep       = "\x1f"
)

// TestMain runs main in a child of this test binary. That is the only way to see
// the real command line being read: the exit code, the output and the log file of
// a run that went through main.
func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv(mainChildEnv); ok {
		os.Args = []string{"SDAutoLogin"}
		if args != "" {
			os.Args = append(os.Args, strings.Split(args, argSep)...)
		}
		main()
		os.Exit(0)
	}
	code := m.Run()
	if instanceDirForTests != "" {
		_ = os.RemoveAll(instanceDirForTests)
	}
	os.Exit(code)
}

// runMain starts the program as a child of this test binary and reports what it
// wrote and the code it ended with. dir is the working directory of the run, so a
// run that resolves a directory of its own cannot leave anything in the tree the
// tests live in. The child is given a deadline: a run that would not end by
// itself is a failure of the test, not a suite that hangs.
func runMain(t *testing.T, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The program is started by the path the system reports for the image that is
	// running, not by os.Args[0]: what started this test may be a name in the
	// current directory or a name found on PATH, and Windows refuses to start
	// either (exec.ErrDot), so a test that re-entered itself through argv[0]
	// would fail there with the program itself at no fault.
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("this test cannot say which program it is: %v", err)
	}
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), mainChildEnv+"="+strings.Join(args, argSep))
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("the program did not end by itself with %v, so the command was never run: %s", args, errOut.String())
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running the program with %v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	return out.String(), errOut.String(), code
}

// instanceDirForTests is the directory the single instance lock of this test
// process is in, and instanceHeld says whether this process holds it. The lock
// cannot be given back, so every test that needs it has to talk about one file.
var (
	instanceOnce        sync.Once
	instanceDirForTests string
	instanceHeld        bool
)

// holdSingleInstance takes the lock of the single instance in a directory of its
// own and reports it. A directory of its own keeps the run under test away from
// the state of the account that started the tests, and a child started by runMain
// inherits the environment, so it finds the same lock held.
func holdSingleInstance(t *testing.T) string {
	t.Helper()
	instanceOnce.Do(func() {
		dir, err := os.MkdirTemp("", "sdautologin-instance-")
		if err != nil {
			t.Errorf("making a directory for the lock: %v", err)
			return
		}
		instanceDirForTests = dir
		// The first candidate for the lock is XDG_RUNTIME_DIR, and a private
		// directory of this process is what the code under test prefers.
		os.Setenv("XDG_RUNTIME_DIR", dir)
		os.Setenv("STATE_DIRECTORY", "")
		instanceHeld = platform.AcquireSingleInstance(false)
	})
	if instanceDirForTests == "" || !instanceHeld {
		t.Skip("this process did not take the single instance lock")
	}
	return instanceDirForTests
}

// A misspelled credential option used to be echoed whole and written into
// logs/app.log, which outlives the run: a typo turned a password from something
// in the process list into something on disk and in whatever collects the log.
// The program now reports the option name and nothing after the '='.
func TestAMisspelledCredentialOptionIsNotWrittenDownAnywhere(t *testing.T) {
	for _, arg := range []string{
		"--pasword=Hunter2",
		"-password=Hunter2",
		"--acount=23D001:Hunter2",
	} {
		home := t.TempDir()
		stdout, stderr, code := runMain(t, home, "--home="+home, arg)
		if code != 2 {
			t.Errorf("%s: the program ended with %d, want 2", arg, code)
		}
		if !strings.Contains(stderr, "unknown option") {
			t.Errorf("%s: the typo was not reported:\n%s", arg, stderr)
		}
		if strings.Contains(stdout+stderr, "Hunter2") || strings.Contains(stdout+stderr, "23D001") {
			t.Errorf("%s: the value of the option was echoed:\n%s%s", arg, stdout, stderr)
		}
		// The log file is the channel a Windows build has and a reader of a
		// service log has, and the run reached it: the error is in there.
		log := readOutput(t, filepath.Join(home, "logs", "app.log"))
		if !strings.Contains(log, "unknown option") {
			t.Errorf("%s: the error was not logged:\n%s", arg, log)
		}
		if strings.Contains(log, "Hunter2") || strings.Contains(log, "23D001") {
			t.Errorf("%s: the value of the option reached the log file:\n%s", arg, log)
		}
	}
}

// A command written after a shortcut word used to be dropped without a word:
// `-cli login` ran the background mode and never mentioned login, and
// `-show config` started the tray. Both are the requested action being replaced
// by another one, quietly.
func TestACommandAfterAShortcutWordIsNotDropped(t *testing.T) {
	home := t.TempDir()
	_, stderr, code := runMain(t, home, "--home="+home, "--no-log", "-cli", "login")
	if code != 2 {
		t.Errorf("-cli login ended with %d, want 2 for a line that names two commands:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "takes no further argument") {
		t.Errorf("the leftover word was not reported:\n%s", stderr)
	}

	// The config flag in front of its command is the form that has to work: the
	// flag is moved next to the command, so -show config and config --show are
	// one request.
	dir := t.TempDir()
	stdout, stderr, code := runMain(t, dir, "--home="+dir, "--no-log", "-show", "config")
	if code != 0 {
		t.Errorf("-show config ended with %d, want 0:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "config_file") {
		t.Errorf("-show config did not read the configuration:\n%s", stdout)
	}
	if strings.Contains(stdout, "Usage:") {
		t.Errorf("-show config answered with something else:\n%s", stdout)
	}

	// A word that belongs to a command, with no command named at all, is refused
	// rather than starting the tray without a word about it.
	other := t.TempDir()
	_, stderr, code = runMain(t, other, "--home="+other, "--no-log", "--show")
	if code != 2 || !strings.Contains(stderr, "config command") {
		t.Errorf("--show alone ended with %d, want 2 and a word about the config command:\n%s", code, stderr)
	}
}

// An address where a command belongs is what --app and --settings look like when
// the '=' was left out. Both ways of refusing it name it as an address.
func TestAnAddressWhereACommandBelongsIsNamedAsAnAddress(t *testing.T) {
	dir := t.TempDir()
	app := ""
	for _, c := range []struct {
		what string
		opts options
	}{
		{"an address on its own", options{}},
		{"an address after a bare --app", options{app: &app}},
	} {
		var code int
		_, stderr := captureOutput(t, func() { code = runCommand([]string{"0.0.0.0:8088"}, dir, c.opts) })
		if code != 2 {
			t.Errorf("%s: exit code %d, want 2:\n%s", c.what, code, stderr)
		}
		if !strings.Contains(stderr, "--app=0.0.0.0:8088") {
			t.Errorf("%s: the address was not named as an address:\n%s", c.what, stderr)
		}
	}
}

// ---------------------------------------------------------------------------
// The parser

// An unknown option is reported by its name alone. The value may be a mistyped
// password, and the log line outlives the terminal it was printed on.
func TestAnUnknownOptionIsReportedWithoutItsValue(t *testing.T) {
	for _, arg := range []string{
		"--pasword=Hunter2",
		"-password=Hunter2",
		"--acount=23D001:Hunter2",
		"--setings=0.0.0.0:8088",
	} {
		_, _, err := parseOptions([]string{arg})
		if err == nil {
			t.Errorf("parseOptions(%q) accepted an option it does not define", arg)
			continue
		}
		if strings.Contains(err.Error(), "Hunter2") || strings.Contains(err.Error(), "23D001") {
			t.Errorf("parseOptions(%q) echoed the value: %v", arg, err)
		}
		name, _, _ := strings.Cut(arg, "=")
		if !strings.Contains(err.Error(), name) {
			t.Errorf("parseOptions(%q) did not name the option: %v", arg, err)
		}
	}

	// A long argument is bounded: one line of the log cannot be filled with it.
	_, _, err := parseOptions([]string{"--" + strings.Repeat("x", 500) + "=SECRET"})
	if err == nil {
		t.Fatal("a long unknown option was accepted")
	}
	if strings.Contains(err.Error(), "SECRET") || len(err.Error()) > 120 {
		t.Errorf("the error carries too much of the argument: %d bytes: %v", len(err.Error()), err)
	}
}

// --save=false is how a script says a switch is off, and it used to be read as
// --save: the user wrote "do not keep the password" and the password was written
// into config.json. A value that is neither true nor false is now refused rather
// than read as "on".
func TestASwitchWrittenWithAValueIsReadAsThatValue(t *testing.T) {
	const off = "--save=false"
	for _, args := range [][]string{
		{"--save"}, {"--save=true"}, {"--save=1"}, {"--save=TRUE"},
	} {
		opts, _, err := parseOptions(args)
		if err != nil || !opts.save {
			t.Errorf("parseOptions(%v) save = %v, %v, want the switch on", args, opts.save, err)
		}
	}
	for _, args := range [][]string{
		{off}, {"--save=0"}, {"--save=False"}, {"--no-log=false"}, {"--quiet=false"},
		{"--json=false"}, {"--install=false"}, {"--save", off},
	} {
		opts, _, err := parseOptions(args)
		if err != nil {
			t.Errorf("parseOptions(%v): %v", args, err)
			continue
		}
		if opts.save || opts.noLog || opts.quiet || opts.json || opts.install {
			t.Errorf("parseOptions(%v) read a switch as on", args)
		}
	}
	for _, args := range [][]string{
		{"--save=maybe"}, {"--save="}, {"--install=yes"}, {"--quiet=2"},
	} {
		if _, _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%v) accepted a value it does not know", args)
		}
	}

	// --app keeps its own meaning: an address is a value there, not a switch.
	opts, _, err := parseOptions([]string{"--app=0.0.0.0:8088"})
	if err != nil || opts.app == nil || *opts.app != "0.0.0.0:8088" {
		t.Errorf("parseOptions(--app=0.0.0.0:8088) app = %v, %v", opts.app, err)
	}
}

// The whole way in: the options are parsed, applied and saved, which is the path
// that wrote the password down for --save=false.
func TestSayingNotToSaveDoesNotWriteThePasswordDown(t *testing.T) {
	dir := t.TempDir()
	opts, _, err := parseOptions([]string{"--account=s1234567:topsecret", "--save=false"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if _, ok := loadForCLI(dir, opts); !ok {
		t.Fatal("the run was refused")
	}
	written, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "topsecret") {
		t.Errorf("--save=false wrote the password into the configuration:\n%s", written)
	}
}

// A lone -- ends the options, so a word that begins with a dash can be passed
// where a word is read, and - alone is a word rather than a typo.
func TestTheOptionsEndAtADoubleDash(t *testing.T) {
	opts, rest, err := parseOptions([]string{"--username=s1234567", "--", "--json", "-x", "status"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.username == nil || *opts.username != "s1234567" {
		t.Errorf("username = %v, want the option before the terminator", opts.username)
	}
	if opts.json {
		t.Error("--json after -- was read as an option")
	}
	if len(rest) != 3 || rest[0] != "--json" || rest[1] != "-x" || rest[2] != "status" {
		t.Errorf("rest = %v, want the words handed back as they are", rest)
	}

	if _, rest, err := parseOptions([]string{"-"}); err != nil {
		t.Errorf("parseOptions(-): %v", err)
	} else if len(rest) != 1 || rest[0] != "-" {
		t.Errorf("rest = %v, want the - handed back", rest)
	}
}

// A directory named like an option is what a forgotten value makes: --home -cli
// kept the settings in a directory called -cli. The path that says what it means
// is accepted.
func TestAHomeDirectoryNamedLikeAnOptionIsRefused(t *testing.T) {
	for _, args := range [][]string{{"--home", "-cli"}, {"--home=-cli"}, {"--home", "-"}} {
		if _, _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%v) accepted a directory name read as an option", args)
		}
	}
	opts, _, err := parseOptions([]string{"--home=./-cli"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.home == nil || *opts.home != "./-cli" {
		t.Errorf("home = %v, want the path that names a directory", opts.home)
	}
}

// The command is read wherever it was written, and a shortcut next to a different
// command is refused rather than one of them being preferred silently.
func TestTheCommandIsReadWhereverItWasWritten(t *testing.T) {
	for _, c := range []struct {
		args    []string
		command string
		want    []string
	}{
		{[]string{"status"}, "status", nil},
		{[]string{"login", "--json"}, "login", []string{"--json"}},
		{[]string{"-test-login"}, "login", nil},
		{[]string{"-cli"}, "run", nil},
		{[]string{"config", "--show"}, "config", []string{"--show"}},
		{[]string{"-show", "config"}, "config", []string{"-show"}},
		{[]string{"-cli", "login"}, "run", []string{"login"}},
		{[]string{"-cli", "status"}, "run", []string{"status"}},
		{[]string{"-h", "config"}, "help", nil},
		{[]string{"--version"}, "version", nil},
		{[]string{"--show"}, "", []string{"--show"}},
		{nil, "", nil},
	} {
		command, args, err := invocation(c.args)
		if err != nil {
			t.Errorf("invocation(%v): %v", c.args, err)
			continue
		}
		if command != c.command {
			t.Errorf("invocation(%v) command = %q, want %q", c.args, command, c.command)
		}
		if strings.Join(args, " ") != strings.Join(c.want, " ") {
			t.Errorf("invocation(%v) args = %v, want %v", c.args, args, c.want)
		}
	}

	for _, args := range [][]string{{"-cli", "-test-login"}, {"login", "-cli"}} {
		if _, _, err := invocation(args); err == nil {
			t.Errorf("invocation(%v) accepted two commands", args)
		}
	}

	// A shortcut and the command it stands for say the same thing, and the words
	// after them are what the command reads: this one is refused by the command
	// rather than by the dispatch, see checkCommandArgs.
	if command, args, err := invocation([]string{"-cli", "run"}); err != nil || command != "run" || len(args) != 0 {
		t.Errorf("invocation(-cli run) = %q, %v, %v", command, args, err)
	}
}

// A word after a command is either the one flag that command reads or a mistake:
// dropping it left a script waiting for a report that would never come.
func TestAWordAfterACommandIsRefused(t *testing.T) {
	for _, c := range []struct {
		command string
		args    []string
		ok      bool
	}{
		{"status", nil, true},
		{"status", []string{"extra"}, false},
		{"update", []string{"--install"}, false},
		{"config", []string{"--show"}, true},
		{"config", []string{"-show"}, true},
		{"config", []string{"--shwo"}, false},
		{"version", []string{"extra"}, true},
		{"help", []string{"extra"}, true},
		{"staus", []string{"extra"}, true},
	} {
		err := checkCommandArgs(c.command, c.args)
		if (err == nil) != c.ok {
			t.Errorf("checkCommandArgs(%q, %v) = %v, want ok=%v", c.command, c.args, err, c.ok)
		}
	}
}

// The exit code a script reads has to tell a refused password from a portal that
// could not be asked: only the second one is worth retrying, and retrying the
// first is what locks an account.
func TestTheLoginExitCodeTellsTheFailuresApart(t *testing.T) {
	refused := app.LoginTest{Result: portal.LoginResult{RetCode: "1", Message: "wrong account or password"},
		After: portal.Status{Reachable: true, Known: true}}
	answered := app.LoginTest{Result: portal.LoginResult{Message: "result=\"0\", HTTP 200"},
		After: portal.Status{Reachable: true, Known: true}}
	unanswered := app.LoginTest{Result: portal.LoginResult{Message: "request failed: dial"},
		After: portal.Status{Reachable: true}}
	silent := app.LoginTest{Result: portal.LoginResult{Message: "portal unreachable"}}

	for _, c := range []struct {
		what string
		test app.LoginTest
		want int
	}{
		{"the portal accepted the login", app.LoginTest{Result: portal.LoginResult{Success: true}}, loginSignedIn},
		{"the client was already online", app.LoginTest{AlreadyOnline: true}, loginSignedIn},
		{"the portal answered with its ret_code", refused, loginRefused},
		{"the portal answered a usable state and refused", answered, loginRefused},
		{"the portal could not answer at all", unanswered, loginUnreachable},
		{"the portal could not be asked", silent, loginUnreachable},
	} {
		if got := loginExitCode(c.test); got != c.want {
			t.Errorf("%s: loginExitCode = %d, want %d", c.what, got, c.want)
		}
	}
}

// The settings page is protected by the token in the address it is handed out
// with, so the addresses that other machines can reach have to be told from the
// ones that only this machine can.
func TestAnAddressBeyondThisMachineIsToldApart(t *testing.T) {
	for _, c := range []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8088", false},
		{"localhost:8088", false},
		{"[::1]:8088", false},
		{"0.0.0.0:8088", true},
		{":8088", true},
		{"10.0.0.5:8088", true},
		{"[2001:db8::1]:8088", true},
		{"somehost:8088", true},
		{"8088", true},
	} {
		if got := servesBeyondThisMachine(c.addr); got != c.want {
			t.Errorf("servesBeyondThisMachine(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// The password prompt

// A terminal that cannot hide what is typed is not the same thing as one that
// can, and the reader has to be told which one this is: the answer typed visibly
// stays in the scrollback and on whatever records the screen.
func TestThePasswordPromptSaysWhenItCannotHideTheAnswer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	previous := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = previous }()

	var secret string
	stdout, stderr := captureOutput(t, func() {
		secret = promptPassword(bufio.NewReader(f), "")
	})
	if secret != "s3cret" {
		t.Errorf("promptPassword = %q, want what was typed", secret)
	}
	if !strings.Contains(stdout, "password: ") {
		t.Errorf("the prompt does not ask for a password:\n%s", stdout)
	}
	if !strings.Contains(stderr, "cannot hide") || !strings.Contains(stderr, "visible") {
		t.Errorf("a prompt that cannot hide the answer said nothing about it:\n%s", stderr)
	}
	if strings.Contains(stdout+stderr, "s3cret") {
		t.Errorf("the password was printed:\n%s%s", stdout, stderr)
	}
}

// ---------------------------------------------------------------------------
// The credentials on the command line

// The password on the command line is readable from the process list by every
// user of the machine, and it is said once per run rather than per check.
func TestACommandLinePasswordIsWarnedAboutOnce(t *testing.T) {
	secret := "topsecret"
	for _, opts := range []options{
		{password: &secret},
		{accounts: []string{"s1234567:topsecret"}},
	} {
		dir := t.TempDir()
		_, stderr := captureOutput(t, func() {
			if _, ok := loadForCLI(dir, opts); !ok {
				t.Error("the run was refused")
			}
		})
		if got := strings.Count(stderr, "process list"); got != 1 {
			t.Errorf("the warning was printed %d times, want once:\n%s", got, stderr)
		}
		if strings.Contains(stderr, "topsecret") {
			t.Errorf("the warning carries the password:\n%s", stderr)
		}
	}

	// An account named without a password is not a secret.
	_, stderr := captureOutput(t, func() { loadForCLI(t.TempDir(), options{accounts: []string{"s1234567"}}) })
	if strings.Contains(stderr, "process list") {
		t.Errorf("a username on its own was warned about as a password:\n%s", stderr)
	}
}

// What systemd hands over belongs to this run and not to the account: the path is
// used, it is named out loud, and --save does not write the environment of this
// run into config.json where every later run would read it.
func TestTheCredentialSystemdHandedOverIsUsedAndNotWrittenDown(t *testing.T) {
	named := writeSecret(t, "fromthefile\n")
	dir := t.TempDir()
	body := fmt.Sprintf(`{"passwordFile":%q,"accounts":[{"username":"s1234567","isps":[""]}]}`, named)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	credDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(credDir, credentialName), []byte("fromsystemd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", credDir)

	var cfg *config.Config
	var ok bool
	_, stderr := captureOutput(t, func() { cfg, ok = loadForCLI(dir, options{save: true}) })
	if !ok {
		t.Fatal("a run that was handed a credential was refused")
	}
	if cfg.Accounts[0].Password != "fromsystemd" {
		t.Errorf("Password = %q, want the credential", cfg.Accounts[0].Password)
	}
	if !strings.Contains(stderr, credDir) {
		t.Errorf("the password file in use was not named:\n%s", stderr)
	}
	if !strings.Contains(stderr, "replaces the passwordFile") {
		t.Errorf("the file being replaced was not named:\n%s", stderr)
	}

	written, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), credDir) {
		t.Errorf("the credential directory of this run was written into config.json:\n%s", written)
	}
	reread, err := config.LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if reread.PasswordFile != named {
		t.Errorf("passwordFile = %q, want the configured %q", reread.PasswordFile, named)
	}
	if strings.Contains(string(written), "fromsystemd") {
		t.Errorf("the credential was written into config.json:\n%s", written)
	}
}

// A credential file any user of the machine can read is said so: it holds a
// password, and the run itself says which file it read it from instead of
// silently taking a secret from somewhere else.
func TestACredentialAnyUserCanReadIsWarnedAbout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, credentialName)
	if err := os.WriteFile(path, []byte("fromsystemd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", dir)

	_, stderr := captureOutput(t, func() { loadForCLI(t.TempDir(), options{}) })
	if !strings.Contains(stderr, "any user") {
		t.Errorf("a credential readable by any user was not reported:\n%s", stderr)
	}
	if strings.Contains(stderr, "fromsystemd") {
		t.Errorf("the password was printed:\n%s", stderr)
	}
}

// ---------------------------------------------------------------------------
// The commands, against a portal of the test's own

// fakePortal answers the three requests the portal protocol makes, so a command
// can be run against a portal that is not there. login is the body the login
// request answers with, state the body of the session query.
func fakePortal(t *testing.T, login, state string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("a") {
		case "login":
			fmt.Fprint(w, login)
		case "online_list":
			fmt.Fprint(w, state)
		default:
			fmt.Fprint(w, "<script>v4ip = '10.1.2.3'</script>")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// portalConfig points the configuration of a run at the portal named, with one
// account to sign in with.
func portalConfig(t *testing.T, dir, base string) {
	t.Helper()
	body := fmt.Sprintf(`{"portalBaseUrl":%q,"eportalUrl":%q,"accounts":[{"username":"s1234567","password":"topsecret","isps":[""]}]}`,
		base+"/", base+"/")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The status command is the one a monitoring script reads, in both shapes.
func TestTheStatusCommandReadsThePortalAndItsExitCode(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", "")
	for _, c := range []struct {
		what  string
		state string
		code  int
		line  string
	}{
		{"this client is signed in", `{"result":1,"online_ip":"10.1.2.3"}`, 0, "online       : true"},
		{"this client is not signed in", `{"result":0,"online_ip":"10.1.2.3"}`, 3, "online       : false"},
		{"the portal answered nothing usable", `{}`, 1, "state_known  : false"},
	} {
		dir := t.TempDir()
		srv := fakePortal(t, `{"result":0}`, c.state)
		portalConfig(t, dir, srv.URL)

		var code int
		var stdout, stderr string
		captureOutput(t, func() {}) // keeps the swap of the streams in one place
		stdout, stderr = captureOutput(t, func() { code = cmdStatus(dir, options{}) })
		if code != c.code {
			t.Errorf("%s: exit code %d, want %d:\n%s", c.what, code, c.code, stderr)
		}
		if !strings.Contains(stdout, c.line) {
			t.Errorf("%s: the report does not hold %q:\n%s", c.what, c.line, stdout)
		}
	}

	// --json prints the same answer as one line, so a script needs no parser.
	dir := t.TempDir()
	srv := fakePortal(t, `{"result":0}`, `{"result":1,"online_ip":"10.1.2.3"}`)
	portalConfig(t, dir, srv.URL)
	var code int
	stdout, _ := captureOutput(t, func() { code = cmdStatus(dir, options{json: true}) })
	if code != 0 {
		t.Errorf("status --json = %d, want 0", code)
	}
	var report statusReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("status --json is not one line of JSON: %v, %q", err, stdout)
	}
	if !report.Online || report.State != stateOnline {
		t.Errorf("the report is %+v, want this client online", report)
	}
}

// The login command is the one a retry loop runs, so its exit code has to say
// what to do next: 4 for a refused password, 5 for a portal that could not be
// asked, 1 for a run with nothing to sign in with.
func TestTheLoginCommandTellsTheFailuresApart(t *testing.T) {
	holdSingleInstance(t)
	t.Setenv("CREDENTIALS_DIRECTORY", "")

	dir := t.TempDir()
	srv := fakePortal(t, `{"result":0,"ret_code":1,"msg":"wrong account or password"}`, `{"result":0,"online_ip":"10.1.2.3"}`)
	portalConfig(t, dir, srv.URL)
	var code int
	var stdout, stderr string
	stdout, stderr = captureOutput(t, func() { code = cmdLogin(dir, options{}) })
	if code != loginRefused {
		t.Errorf("a refused password = %d, want %d:\n%s", code, loginRefused, stderr)
	}
	if !strings.Contains(stderr, "do not retry") {
		t.Errorf("the reader was not told not to retry:\n%s", stderr)
	}
	if strings.Contains(stdout+stderr, "topsecret") {
		t.Errorf("the password was printed:\n%s%s", stdout, stderr)
	}

	// Nothing answered: retrying later is the right thing to do, and 4 would stop
	// a monitoring script from trying again.
	other := t.TempDir()
	portalConfig(t, other, "http://127.0.0.1:1")
	stderr = ""
	stdout, stderr = captureOutput(t, func() { code = cmdLogin(other, options{}) })
	if code != loginUnreachable {
		t.Errorf("a portal that cannot be asked = %d, want %d:\n%s", code, loginUnreachable, stderr)
	}

	// No account is a run that cannot start, not a portal problem.
	empty := t.TempDir()
	_, stderr = captureOutput(t, func() { code = cmdLogin(empty, options{}) })
	if code != loginCannotRun {
		t.Errorf("a login with no account = %d, want %d:\n%s", code, loginCannotRun, stderr)
	}
}

// A copy that is already running owns the login: a second request from here would
// race its check loop over one session, which the portal keeps one of per address.
func TestALoginStopsWhenAnotherCopyOwnsIt(t *testing.T) {
	dir := holdSingleInstance(t)
	home := t.TempDir()
	_, stderr, code := runMain(t, home, "--home="+home, "--no-log", "-test-login")
	if code != loginAlreadyRunning {
		t.Errorf("a login next to a running copy = %d, want %d:\n%s", code, loginAlreadyRunning, stderr)
	}
	if !strings.Contains(stderr, "another copy") {
		t.Errorf("the reader was not told which copy signs in:\n%s", stderr)
	}
	// The lock is where this test put it: the child found it held. On Windows
	// there is no such file to look for - the single instance is a named mutex
	// of the session (see platform/singleinst_windows.go), and the child found
	// the name taken, which is what the exit code above reports - so the file is
	// only what the Unix-like systems have to show for it.
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(filepath.Join(dir, "sdautologin.lock")); err != nil {
			t.Errorf("the lock this test holds is not where the child looks for it: %v", err)
		}
	}
}

// The run command refuses to start with nothing to sign in with, and refuses a
// configuration it cannot read, both before anything is submitted.
func TestTheRunCommandRefusesToStartWithoutSomethingToSignInWith(t *testing.T) {
	holdSingleInstance(t)
	t.Setenv("CREDENTIALS_DIRECTORY", "")

	dir := t.TempDir()
	var code int
	_, stderr := captureOutput(t, func() { code = cmdRun(dir, options{}) })
	if code != 1 {
		t.Errorf("a run with no account = %d, want 1:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "no account") {
		t.Errorf("the run did not say what it is missing:\n%s", stderr)
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr = captureOutput(t, func() { code = cmdRun(broken, options{}) })
	if code != 1 {
		t.Errorf("a run with a configuration that cannot be read = %d, want 1:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "config error") {
		t.Errorf("the run did not report the configuration:\n%s", stderr)
	}
}

// runCommand is what every command word goes through, so each of them is answered
// here, and a word that names no command is refused rather than run.
func TestRunCommandAnswersEachCommand(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		args []string
		code int
		out  string
		what string
	}{
		{[]string{"version"}, 0, "SDAutoLogin", "version"},
		{[]string{"help"}, 0, "Usage:", "help"},
		{[]string{"staus"}, 2, "unknown command", "a misspelled command"},
		{[]string{"status", "extra"}, 2, "takes no further argument", "a command with a word after it"},
		{[]string{"config", "--show"}, 0, "config_file", "config --show"},
		{[]string{"config", "--shwo"}, 2, "config reads --show", "config with a flag it does not read"},
	} {
		var code int
		var stdout, stderr string
		stdout, stderr = captureOutput(t, func() { code = runCommand(c.args, dir, options{}) })
		if code != c.code {
			t.Errorf("%s: exit code %d, want %d:\n%s", c.what, code, c.code, stderr)
		}
		if !strings.Contains(stdout+stderr, c.out) {
			t.Errorf("%s: %q is not in the answer:\n%s%s", c.what, c.out, stdout, stderr)
		}
	}
}

// ---------------------------------------------------------------------------
// The configuration directory and the wizard

// MkdirAll leaves a directory that is there alone, and this path comes from
// $XDG_CONFIG_HOME and $HOME on the Unix-like systems and from %AppData% on
// Windows: a directory another user can read is not a place for a configuration
// that may hold a password, and what decides whether it is reachable by another
// user is the mode on one platform and the access list on the other (see
// assertDirectoryIsPrivate).
func TestThePerUserConfigDirectoryIsMadePrivate(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "sdautologin")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	// Both spellings of the same place: os.UserConfigDir reads $XDG_CONFIG_HOME
	// on the Unix-like systems and %AppData% on Windows, so a test that set only
	// one of them would read the account's own directory on the other and write
	// into it.
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("AppData", base)

	got, err := perUserDir()
	if err != nil {
		t.Fatalf("perUserDir: %v", err)
	}
	if got != dir {
		t.Errorf("perUserDir = %q, want %q", got, dir)
	}
	assertDirectoryIsPrivate(t, dir)
}

// The directory checks answer what they find, including the case where the name
// is not a directory at all.
func TestTheDirectoryChecksAnswerWhatTheyFind(t *testing.T) {
	if err := writableDir(t.TempDir()); err != nil {
		t.Errorf("a directory of this test was not writable: %v", err)
	}
	if err := writableDir("/proc/self/not-there"); err == nil {
		t.Error("a directory that cannot be written in was accepted")
	}
	// The list systemBinDir holds is the one a POSIX package installs into. There
	// is no equivalent list on Windows: a copy installed under C:\Program Files is
	// refused for a different reason, that the directory cannot be written in (see
	// writableDir), which is what sends it to the per-user directory, so there is
	// nothing for this function to answer there.
	if runtime.GOOS != "windows" {
		for _, c := range []struct {
			dir  string
			want bool
		}{
			{"/usr/bin", true}, {"/usr/local/sbin", true}, {"/opt/sdautologin", false}, {"/usr", false},
		} {
			if got := systemBinDir(c.dir); got != c.want {
				t.Errorf("systemBinDir(%q) = %v, want %v", c.dir, got, c.want)
			}
		}
	}

	file := filepath.Join(t.TempDir(), "sdautologin")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := makePrivate(file); err == nil {
		t.Errorf("makePrivate(%s) accepted a name that is not a directory", file)
	}
}

// runWizardWithAnswers is the way in on a machine with neither desktop nor
// browser. Its answers are read from stdin, so the whole wizard can be run here.
func runWizardWithAnswers(t *testing.T, dir, answers string) (stdout, stderr string, err error) {
	t.Helper()
	path := filepath.Join(dir, "stdin")
	if err := os.WriteFile(path, []byte(answers), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	previous := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = previous }()

	cfg, err := config.LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr = captureOutput(t, func() { err = runSetupWizard(cfg) })
	return stdout, stderr, err
}

// The wizard used to turn the password back on whatever the configuration said: a
// run that came here to change the check interval wrote a plain text password
// into config.json. It now asks, and Enter keeps what is in force.
func TestTheWizardAsksBeforeItWritesThePasswordDown(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, err := runWizardWithAnswers(t, dir, "3\ns1234567\ntopsecret\n1\n\nn\n")
	if err != nil {
		t.Fatalf("the wizard failed: %v", err)
	}
	if !strings.Contains(stderr, "not written down") {
		t.Errorf("the wizard did not say what the answer means:\n%s", stderr)
	}
	written, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "topsecret") {
		t.Errorf("the answer no still wrote the password into the configuration:\n%s", written)
	}
	reread, err := config.LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if reread.RememberPassword {
		t.Error("the wizard turned the password back on")
	}

	// Enter keeps the answer that is in force, which on a first run is the yes
	// the wizard needs: the same run without an answer writes the password down.
	other := t.TempDir()
	if _, _, err := runWizardWithAnswers(t, other, "3\ns1234567\ntopsecret\n1\n\n\n"); err != nil {
		t.Fatalf("the wizard failed: %v", err)
	}
	saved, err := config.LoadConfig(filepath.Join(other, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !saved.RememberPassword {
		t.Error("Enter at the question turned the password off")
	}
	if saved.Accounts[0].Password != "topsecret" {
		t.Errorf("the wizard did not write the password it was given: %+v", saved.Accounts)
	}

	// The password itself is never printed.
	if strings.Contains(stdout, "topsecret") {
		t.Errorf("the wizard printed the password:\n%s", stdout)
	}
}

// An account that is missing one of the two halves is not written down at all:
// the run it would leave behind reports that there is no account to sign in with,
// pointing at the wizard that just answered that all is well.
func TestTheWizardRefusesAnAccountItCannotSignInWith(t *testing.T) {
	dir := t.TempDir()
	_, stderr, err := runWizardWithAnswers(t, dir, "3\n\n\n1\n\n\n")
	if err == nil {
		t.Fatal("the wizard accepted an account with neither a username nor a password")
	}
	if !strings.Contains(stderr, "a username and a password") {
		t.Errorf("the wizard did not say what the account is missing:\n%s", stderr)
	}
	wrote, readErr := os.ReadFile(filepath.Join(dir, "config.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(wrote), `"username"`) {
		t.Errorf("an account that cannot sign in was written down:\n%s", wrote)
	}
}

// A yes or no question keeps what is in force on anything it does not understand,
// which is what Enter and a mistyped answer have in common.
func TestAYesOrNoQuestionKeepsTheAnswerInForce(t *testing.T) {
	for _, c := range []struct {
		answer  string
		current bool
		want    bool
	}{
		{"y\n", false, true},
		{"yes\n", false, true},
		{"Y\n", true, true},
		{"n\n", true, false},
		{"no\n", false, false},
		{"\n", true, true},
		{"\n", false, false},
		{"maybe\n", false, false},
		{"maybe\n", true, true},
	} {
		in := bufio.NewReader(strings.NewReader(c.answer))
		var got bool
		captureOutput(t, func() { got = promptBool(in, "write it down", c.current) })
		if got != c.want {
			t.Errorf("promptBool(%q, %v) = %v, want %v", c.answer, c.current, got, c.want)
		}
	}
}

// accountGap names what an entry is missing, so the wizard can say which half.
func TestAnAccountGapNamesWhatIsMissing(t *testing.T) {
	for _, c := range []struct {
		username string
		password string
		want     string
	}{
		{"", "", "a username and a password"},
		{"", "secret", "a username"},
		{"s1234567", "", "a password"},
		{"s1234567", "secret", ""},
	} {
		if got := accountGap(config.Account{Username: c.username, Password: c.password}); got != c.want {
			t.Errorf("accountGap(%q, %q) = %q, want %q", c.username, c.password, got, c.want)
		}
	}
}
