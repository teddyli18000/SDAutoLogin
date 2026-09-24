package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sdautologin/internal/config"
	"sdautologin/internal/portal"
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
	// A misspelled option has to fail rather than be passed on: a monitoring
	// script that writes --jason would otherwise be handed a plain status report
	// and read it as the answer it asked for.
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
	// What begins with a dash without being an option belongs to the caller:
	// the shortcuts and the flag of the config command, which the program reads
	// out of what is left. None of them may be mistaken for a typo.
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

// emptyOptions reports whether nothing at all was read out of the arguments.
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
	// --save takes no value, so it has to be recognised by itself instead of
	// being left behind as a stray argument.
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

// A password that is read from a file is the form a service uses: the command
// line of a process is readable by every user that can see the process list, and
// the configuration file is the file the secret is being kept out of.
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

// Only the line ending comes off the file. A password may end in a space, and a
// space that was trimmed away is a password quietly different from the one that
// was written.
func TestAPasswordFileKeepsEverythingButTheLineEnding(t *testing.T) {
	path := writeSecret(t, "two spaces  \r\nsecond line\n")

	got, err := readPasswordFile(path)
	if err != nil {
		t.Fatalf("readPasswordFile: %v", err)
	}
	if got != "two spaces  " {
		t.Errorf("readPasswordFile = %q, want %q", got, "two spaces  ")
	}
}

func TestAnEmptyPasswordFileIsRefused(t *testing.T) {
	path := writeSecret(t, "")
	if _, err := readPasswordFile(path); err == nil {
		t.Error("a file with nothing in it was taken for a password")
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
// applied for it to be read at all.
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

// The whole way in, not applyTo alone: loadForCLI is what decides whether the
// options are applied, and a real start has to end up with the password the
// service manager handed over.
func TestLoadForCLITakesThePasswordSystemdHandedOver(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, credentialName), []byte("fromsystemd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", dir)

	// The account is written down without a password, which is the point of
	// handing the secret over separately.
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

// An account named without a service type is the campus network, and it has to be
// usable as it stands: a run given this is not saved, so it never passes through
// the normalization that would fill the service type in, and an entry without one
// is an entry nothing can be submitted for.
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
// after the first are left where they are: the fallback order is not what this
// run was about.
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

// --save writes what was named, and a password read from a file was named just
// as much as one written on the command line: a configuration with the passwords
// turned off must not end up with an account that has none.
func TestSavingAPasswordFromAFileRemembersIt(t *testing.T) {
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

	reread, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reread.RememberPassword {
		t.Error("the password was not written down after --save")
	}
	if len(reread.Accounts) != 1 || reread.Accounts[0].Password != "fromthefile" {
		t.Errorf("Accounts = %+v, want the password from the file", reread.Accounts)
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
// file has nowhere to go and is refused rather than silently dropped.
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
	// What a provisioning script names is the accounts to use, in the order it
	// names them, rather than an addition to whatever the file held.
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
	// No service type at all is the campus network, and an entry with none
	// cannot be submitted at all: the suffix is filled in here rather than left
	// to a normalization that a run without --save never reaches.
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
		// Naming no service at all is the campus network itself, which is an
		// empty suffix rather than no suffix: an entry without one is not
		// something that can be submitted.
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

	// A value that names no account, and one with two @ in it, which cannot be
	// told apart from a password that holds one, are refused rather than guessed
	// at. The message never repeats the value, since it holds the password.
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

	// No --home at all, or an empty one, leaves the choice to the installation
	// directory, which a portable copy relies on.
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

	// The spaced form belongs to the options that need a value, which --app is
	// not one of, so the address is left over as a stray argument and reported
	// as one instead of being swallowed.
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
// what an entry is allowed to carry. It outlives the run, in a registry value or
// a file anyone can read, so the password must not be in it, and the account
// options belong to the run rather than to the entry. What has to be repeated is
// --home: an entry without it starts on a fresh configuration beside the
// executable, which looks from the outside like the account was lost.
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

	// An empty --home is the same as none at all: it would name no directory.
	empty := ""
	if got := autostartArguments(options{home: &empty}); got != nil {
		t.Errorf("autostartArguments(--home=) = %q, want nothing", got)
	}
}

// TestTheTrayRefusesAnOptionItCannotHonour covers the mode that names no command.
// --json and --settings belong to the status and run commands, and a tray run
// that quietly dropped them would leave a script waiting for a report that never
// comes, or a caller waiting for a page nothing serves.
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

	// What the tray does honour: the options it was started with, and the two
	// that describe the program itself.
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

	// A service has nobody to answer the setup wizard, so a run that could only
	// report the same failure is refused rather than started.
	if err := checkAccountForRun(empty, options{}); err == nil {
		t.Error("a run with no account and no settings page was accepted")
	}

	// With the page served it is started: that page is the way in on a machine
	// whose operator has no other one, and the checks report the missing account
	// until one is written there.
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

	// The tray opens a window of its own, so a build that has none has to refuse
	// the option instead of starting up and ignoring it.
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
