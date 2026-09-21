package main

import (
	"encoding/json"
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
		if opts != (options{}) {
			t.Errorf("parseOptions(%v) read an option out of %v", args, rest)
		}
	}
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
	// --save takes no value of its own, so it has to be recognised on its own
	// instead of being left behind as a stray argument.
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

func TestParseOptionsKeepsAnExplicitlyEmptyValue(t *testing.T) {
	// An empty suffix is how the campus network itself is asked for: it has no
	// operator suffix, and leaving the option out would keep the configured one.
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
	var opts options
	if opts.given() {
		t.Error("empty options must not count as given")
	}
}

func TestApplyToOnlyChangesWhatWasGiven(t *testing.T) {
	cfg := &config.Config{Username: "old", Password: "old", AccountSuffix: "@cucc"}
	password := "new"

	options{password: &password}.applyTo(cfg)

	if cfg.Password != "new" {
		t.Errorf("Password = %q, want the override", cfg.Password)
	}
	if cfg.Username != "old" || cfg.AccountSuffix != "@cucc" {
		t.Errorf("an option that was not given changed the configuration: %+v", cfg)
	}
}

func TestApplyToSetsEverythingThatWasGiven(t *testing.T) {
	cfg := &config.Config{AccountPrefix: ",0,"}
	username, password, isp := "s1234567", "p@ss", "@zgyd"

	options{username: &username, password: &password, isp: &isp}.applyTo(cfg)

	if cfg.Username != username || cfg.Password != password || cfg.AccountSuffix != isp {
		t.Errorf("configuration = %+v", cfg)
	}
	if got := cfg.Account(); got != ",0,s1234567@zgyd" {
		t.Errorf("Account() = %q, want the overridden account", got)
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
	// directory, which is what a portable copy relies on.
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

	// The field names are a promise to whoever writes the monitoring script, so
	// the whole set is pinned down here.
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
