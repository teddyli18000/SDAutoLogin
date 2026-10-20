package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"sdautologin/internal/update"
)

func TestLoadConfigCreatesAFileWithDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Path() != path {
		t.Errorf("Path() = %q, want %q", cfg.Path(), path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the default config was not written: %v", err)
	}
	if cfg.PortalBaseURL != "http://10.9.1.3/" || cfg.EportalURL != "http://10.9.1.3:801/eportal/" {
		t.Errorf("unexpected endpoints: %q %q", cfg.PortalBaseURL, cfg.EportalURL)
	}
	if cfg.CheckIntervalSeconds != 15 || cfg.HTTPTimeoutSeconds != 6 {
		t.Errorf("unexpected timeouts: interval=%d timeout=%d", cfg.CheckIntervalSeconds, cfg.HTTPTimeoutSeconds)
	}
	if !cfg.AutoLogin || !cfg.CheckInternet || !cfg.VerboseLog || !cfg.RememberPassword || !cfg.LogToFile {
		t.Errorf("every boolean default is meant to be on: %+v", cfg)
	}
	if !cfg.SwitchToPreferredService {
		t.Error("SwitchToPreferredService is off by default, so the order of an entry's service types would not be followed for a session that is already up")
	}
	if len(cfg.ProbeURLs) != 3 {
		t.Errorf("ProbeURLs = %v, want the three default probes", cfg.ProbeURLs)
	}
	if cfg.Language != "auto" {
		t.Errorf("Language = %q, want auto", cfg.Language)
	}
}

func TestLoadConfigReadsAFileWithABOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"username":"s1234567","password":"p@ss"}`)...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a UTF-8 BOM must not stop the parse: %v", err)
	}
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].Username != "s1234567" || cfg.Accounts[0].Password != "p@ss" {
		t.Errorf("the account in the file was not read: %+v", cfg.Accounts)
	}
	// Fields the file leaves out keep their default, so an older file survives
	// a new release.
	if cfg.CheckIntervalSeconds != 15 {
		t.Errorf("CheckIntervalSeconds = %d, want the default 15", cfg.CheckIntervalSeconds)
	}
}

func TestLoadConfigMovesTheOneAccountOfAnOlderFileIntoTheList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	old := `{"username":"s1234567","password":"p@ss","accountSuffix":"@zgyd"}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Accounts) != 1 {
		t.Fatalf("Accounts = %+v, want the one account of the old file", cfg.Accounts)
	}
	entry := cfg.Accounts[0]
	if entry.Username != "s1234567" || entry.Password != "p@ss" {
		t.Errorf("the account was not carried over: %+v", entry)
	}
	// The suffix of the old file was the one service type of that account.
	if len(entry.ISPs) != 1 || entry.ISPs[0] != "@zgyd" {
		t.Errorf("ISPs = %v, want the suffix of the old file", entry.ISPs)
	}

	// Saving it again writes the list and drops the old fields, so a file is
	// never half in one shape and half in the other.
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("the file must stay valid JSON: %v", err)
	}
	for _, gone := range []string{"username", "password", "accountSuffix"} {
		if _, ok := stored[gone]; ok {
			t.Errorf("the top level field %q of the old shape is still written: %s", gone, data)
		}
	}
	if _, ok := stored["accounts"]; !ok {
		t.Errorf("the login list was not written: %s", data)
	}
}

// TestAnOlderFileSignsInAsItDidBefore pins the upgrade path: a file written by
// a version that kept one account has to yield the same user_account and the
// same verdict on whether it can sign in. An upgrade that changed either would
// leave a working installation signing in as the wrong account, or as none.
func TestAnOlderFileSignsInAsItDidBefore(t *testing.T) {
	cases := []struct {
		name string
		file string
		// submitted is the user_account the older version sent:
		// AccountPrefix + username + accountSuffix.
		submitted  string
		password   string
		configured bool
	}{
		{
			"with a service type",
			`{"username":"s1234567","password":"p@ss","accountSuffix":"@zgyd"}`,
			",0,s1234567@zgyd", "p@ss", true,
		},
		{
			"on the campus network itself",
			`{"username":"s1234567","password":"p@ss","accountSuffix":""}`,
			",0,s1234567", "p@ss", true,
		},
		{
			"from a file that never had the suffix field",
			`{"username":"s1234567","password":"p@ss"}`,
			",0,s1234567", "p@ss", true,
		},
		{
			"with a password that was not remembered",
			`{"username":"s1234567","password":"","rememberPassword":false,"accountSuffix":"@cucc"}`,
			"", "", false,
		},
		{
			"with an account that was never filled in",
			`{"username":"","password":"","accountSuffix":""}`,
			"", "", false,
		},
	}

	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(c.file), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}

		if got := cfg.Configured(); got != c.configured {
			t.Errorf("%s: Configured() = %v, want %v", c.name, got, c.configured)
		}
		attempts := cfg.Attempts()
		if !c.configured {
			if len(attempts) != 0 {
				t.Errorf("%s: Attempts() = %+v, want none", c.name, attempts)
			}
			continue
		}
		if len(attempts) != 1 {
			t.Fatalf("%s: Attempts() = %+v, want the one entry of the old file", c.name, attempts)
		}
		a := attempts[0]
		if got := cfg.Account(a.Username, a.ISP); got != c.submitted {
			t.Errorf("%s: user_account = %q, want %q, the value the old version submitted",
				c.name, got, c.submitted)
		}
		if a.Index != 0 {
			t.Errorf("%s: Index = %d, want the first entry", c.name, a.Index)
		}
		if a.Password != c.password {
			t.Errorf("%s: Password = %q, want %q", c.name, a.Password, c.password)
		}
	}
}

// TestReadingAnOlderFileLeavesItAlone keeps an upgrade reversible: the list is
// only written when something is saved, so a file a newer version has read can
// still be read by the version that wrote it.
func TestReadingAnOlderFileLeavesItAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	older := `{"username":"s1234567","password":"p@ss","accountSuffix":"@zgyd"}`
	if err := os.WriteFile(path, []byte(older), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != older {
		t.Errorf("reading rewrote the file:\n got %s\nwant %s", stored, older)
	}

	// Saving is what rewrites it, so the file ends up in one shape.
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	stored, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) == older {
		t.Error("saving left the file in the old shape")
	}
}

func TestLoadConfigRejectsBrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Error("LoadConfig accepted a file that is not JSON")
	}
}

func TestNormalizeClampsOutOfRangeValues(t *testing.T) {
	cfg := &Config{CheckIntervalSeconds: 1, HTTPTimeoutSeconds: 0, Language: "de"}

	cfg.normalize()

	if cfg.CheckIntervalSeconds != 5 {
		t.Errorf("CheckIntervalSeconds = %d, want the minimum 5", cfg.CheckIntervalSeconds)
	}
	if cfg.HTTPTimeoutSeconds != 2 {
		t.Errorf("HTTPTimeoutSeconds = %d, want the minimum 2", cfg.HTTPTimeoutSeconds)
	}
	if cfg.Language != "auto" {
		t.Errorf("Language = %q, want an unknown language to fall back to auto", cfg.Language)
	}
	if cfg.PortalBaseURL == "" || cfg.EportalURL == "" || cfg.JsVersion == "" {
		t.Errorf("empty addresses must fall back to the defaults: %+v", cfg)
	}
	if cfg.Accounts == nil {
		t.Error("Accounts must be an empty list so the page can render it")
	}
}

// TestNormalizeClampsValuesThatWouldWrapTheDuration covers the other end of the
// range: the upper bounds, whose reason is given at their definitions.
func TestNormalizeClampsValuesThatWouldWrapTheDuration(t *testing.T) {
	// A value far past the bound that still fits in an int on every platform
	// the program is built for, so the clamp is what has to hold here.
	const tooLarge = 1 << 30
	cfg := &Config{CheckIntervalSeconds: tooLarge, HTTPTimeoutSeconds: tooLarge}

	cfg.normalize()

	if cfg.CheckIntervalSeconds != maxCheckIntervalSeconds {
		t.Errorf("CheckIntervalSeconds = %d, want it held to %d", cfg.CheckIntervalSeconds, maxCheckIntervalSeconds)
	}
	if cfg.HTTPTimeoutSeconds != maxHTTPTimeoutSeconds {
		t.Errorf("HTTPTimeoutSeconds = %d, want it held to %d", cfg.HTTPTimeoutSeconds, maxHTTPTimeoutSeconds)
	}
}

func TestNormalizeGivesAnEntryAServiceType(t *testing.T) {
	// An entry that names no service is the campus network itself, the portal
	// dropdown's first choice.
	cfg := &Config{Accounts: []Account{{Username: " s1234567 ", ISPs: nil}}}
	cfg.normalize()

	entry := cfg.Accounts[0]
	if entry.Username != "s1234567" {
		t.Errorf("Username = %q, want the trimmed account", entry.Username)
	}
	if len(entry.ISPs) != 1 || entry.ISPs[0] != "" {
		t.Errorf("ISPs = %v, want one empty suffix for the campus network", entry.ISPs)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	errs := (&Config{}).Validate()

	for _, field := range []string{
		"accounts", "checkIntervalSeconds",
		"httpTimeoutSeconds", "portalBaseUrl", "eportalUrl",
	} {
		if errs[field] == "" {
			t.Errorf("Validate() did not report %s, got %v", field, errs)
		}
	}
}

func TestValidateAddressesTheEntryItComplainsAbout(t *testing.T) {
	// The keys carry the position of the entry, so the settings page can put the
	// message next to the input it is about.
	cfg := defaultConfig()
	cfg.Accounts = []Account{
		{Username: "s1234567", Password: "p@ss", ISPs: []string{""}},
		{Username: "  ", Password: "", ISPs: []string{""}},
	}

	errs := cfg.Validate()

	if errs["accounts.0.username"] != "" || errs["accounts.0.password"] != "" {
		t.Errorf("the first entry is complete but was reported: %v", errs)
	}
	if errs["accounts.1.username"] == "" {
		t.Errorf("the blank account of the second entry was not reported: %v", errs)
	}
	if errs["accounts.1.password"] == "" {
		t.Errorf("the missing password of the second entry was not reported: %v", errs)
	}
}

func TestValidateLeavesADisabledEntryAlone(t *testing.T) {
	// Taking a half written entry out of the running has to be possible without
	// deleting it, so an entry that is disabled is not asked to be complete.
	cfg := defaultConfig()
	cfg.Accounts = []Account{{Username: "", Password: "", Disabled: true, ISPs: []string{""}}}

	if errs := cfg.Validate(); len(errs) != 0 {
		t.Errorf("Validate() = %v, want no errors for a disabled entry", errs)
	}
}

func TestValidateAcceptsTheDefaults(t *testing.T) {
	cfg := defaultConfig()
	cfg.Accounts = []Account{{Username: "s1234567", Password: "p@ss", ISPs: []string{""}}}

	if errs := cfg.Validate(); len(errs) != 0 {
		t.Errorf("Validate() = %v, want no errors", errs)
	}
}

func TestValidateSkipsBlankProbesAndReportsTheBadOnes(t *testing.T) {
	cfg := defaultConfig()
	cfg.Accounts = []Account{{Username: "s1234567", Password: "p@ss", ISPs: []string{""}}}
	// A blank entry is an empty row in the settings page, and it is dropped
	// before saving.
	cfg.ProbeURLs = []string{"http://ok.example/", "http://", "", "@@@"}

	errs := cfg.Validate()

	if _, ok := errs["probeUrls.0"]; ok {
		t.Errorf("the first probe is valid: %v", errs)
	}
	for _, field := range []string{"probeUrls.1", "probeUrls.3"} {
		if errs[field] == "" {
			t.Errorf("Validate() did not report %s, got %v", field, errs)
		}
	}
	if _, ok := errs["probeUrls.2"]; ok {
		t.Errorf("a blank probe is skipped, not reported: %v", errs)
	}
}

func TestValidHTTPURL(t *testing.T) {
	valid := []string{
		"http://10.9.1.3/",
		"https://example.com/path?q=1",
		"  http://padded.example  ",
	}
	for _, raw := range valid {
		if !ValidHTTPURL(raw) {
			t.Errorf("ValidHTTPURL(%q) = false, want true", raw)
		}
	}

	invalid := []string{
		"",
		"   ",
		"/relative/path",
		"example.com",
		"http://",
		"ftp://example.com/",
	}
	for _, raw := range invalid {
		if ValidHTTPURL(raw) {
			t.Errorf("ValidHTTPURL(%q) = true, want false", raw)
		}
	}
}

func TestCompactProbesTrimsAndDropsBlanks(t *testing.T) {
	got := CompactProbes([]string{"  http://a/  ", "", "   ", "http://b/"})
	want := []string{"http://a/", "http://b/"}

	if len(got) != len(want) {
		t.Fatalf("CompactProbes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CompactProbes[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(CompactProbes(nil)) != 0 {
		t.Error("CompactProbes(nil) should be empty")
	}
}

func TestCompactSuffixesKeepsTheOrderAndDropsTheRepeats(t *testing.T) {
	got := CompactSuffixes([]string{" @zgyd ", "", "@cucc", "@zgyd", ""})
	want := []string{"@zgyd", "", "@cucc"}

	if len(got) != len(want) {
		t.Fatalf("CompactSuffixes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CompactSuffixes[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAccountAndOperatorName(t *testing.T) {
	cfg := &Config{AccountPrefix: ",0,"}

	if got := cfg.Account("s1234567", "@zgyd"); got != ",0,s1234567@zgyd" {
		t.Errorf("Account() = %q", got)
	}
	if got := OperatorName("@zgyd"); got != "China Mobile" {
		t.Errorf("OperatorName() = %q, want the operator the suffix belongs to", got)
	}
	if got := OperatorName("@unknown"); got != "custom(@unknown)" {
		t.Errorf("OperatorName() = %q, want a custom suffix to be shown as is", got)
	}
}

func TestAttemptsAreFlattenedInPriorityOrder(t *testing.T) {
	cfg := &Config{Accounts: []Account{
		{Username: "first", Password: "p", ISPs: []string{"@zgyd", "@cucc"}},
		{Username: "second", Password: "p", ISPs: []string{""}},
		// An entry that is disabled takes no part, and neither does one that
		// cannot sign in.
		{Username: "disabled", Password: "p", Disabled: true, ISPs: []string{""}},
		{Username: "nopassword", ISPs: []string{""}},
	}}

	got := cfg.Attempts()
	want := []struct {
		index int
		isp   string
	}{
		{0, "@zgyd"}, {0, "@cucc"}, {1, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("Attempts() = %+v, want %d of them", got, len(want))
	}
	for i, w := range want {
		if got[i].Index != w.index || got[i].ISP != w.isp {
			t.Errorf("Attempts()[%d] = entry %d %q, want entry %d %q",
				i, got[i].Index, got[i].ISP, w.index, w.isp)
		}
	}
	if got[0].Key() == got[1].Key() {
		t.Error("two service types of one account must not share a key")
	}
	if got[0].Entry() != "first (China Mobile)" {
		t.Errorf("Entry() = %q", got[0].Entry())
	}
}

func TestMatchFindsTheEntryAnOnlineAccountBelongsTo(t *testing.T) {
	cfg := &Config{AccountPrefix: ",0,", Accounts: []Account{
		{Username: "first", Password: "p", ISPs: []string{"@zgyd", "@cucc"}},
		{Username: "second", Password: "p", ISPs: []string{""}},
	}}

	cases := []struct {
		reported string
		index    int
		isp      string
	}{
		{",0,first@zgyd", 0, "@zgyd"},
		{",0,first@cucc", 0, "@cucc"},
		{",0,second", 1, ""},
		{"first@zgyd", 0, "@zgyd"},
		{",0,stranger@zgyd", -1, ""},
		{"", -1, ""},
	}
	for _, c := range cases {
		index, isp := cfg.Match(c.reported)
		if index != c.index || isp != c.isp {
			t.Errorf("Match(%q) = %d %q, want %d %q", c.reported, index, isp, c.index, c.isp)
		}
	}
}

// TestMatchIsNotFooledByAnEntryWithoutAService covers the entry the command line
// can leave behind, one that names an account and no service type. It cannot be
// submitted, but the portal still reports the account as the one signed in, and
// matching it must not read a service type that is not there.
func TestMatchIsNotFooledByAnEntryWithoutAService(t *testing.T) {
	cfg := &Config{AccountPrefix: ",0,", Accounts: []Account{
		{Username: "23D001", Password: "p"},
	}}

	index, isp := cfg.Match("23D001")
	if index != 0 || isp != "" {
		t.Errorf("Match = %d %q, want the entry and no service type", index, isp)
	}
	if index, isp := cfg.Match(",0,23D001@zgyd"); index != -1 || isp != "" {
		t.Errorf("Match of a suffixed account = %d %q, want no match", index, isp)
	}
}

// TestMatchReadsABareAccountAsTheServiceTypeWithoutASuffix covers what the portal
// echoes: the account a login was submitted with, written back as it was sent. A
// bare account is the campus network itself, so reading it as the entry's first
// service type would label the session with the wrong operator.
func TestMatchReadsABareAccountAsTheServiceTypeWithoutASuffix(t *testing.T) {
	cfg := &Config{AccountPrefix: ",0,", Accounts: []Account{
		{Username: "23D001", Password: "p", ISPs: []string{"@zgyd", ""}},
	}}

	if index, isp := cfg.Match("23D001"); index != 0 || isp != "" {
		t.Errorf("Match of a bare account = %d %q, want the entry and the campus network", index, isp)
	}
	if index, isp := cfg.Match(",0,23D001@zgyd"); index != 0 || isp != "@zgyd" {
		t.Errorf("Match of a suffixed account = %d %q, want that service type", index, isp)
	}
}

// TestPreferredForNamesWhatASessionShouldBeMovedOnto covers what the order of an
// entry's service types means for a session that is already up: the first one of
// that entry. Nothing is named when there is nothing to move onto, so a working
// session is not signed in again for no reason.
func TestPreferredForNamesWhatASessionShouldBeMovedOnto(t *testing.T) {
	campus := func() Account {
		return Account{Username: "23D001", Password: "p", ISPs: []string{"", "@zgyd"}}
	}
	mobile := func() Account {
		return Account{Username: "23D001", Password: "p", ISPs: []string{"@zgyd", ""}}
	}

	cases := []struct {
		name     string
		entry    Account
		reported string
		want     string
		moved    bool
	}{
		{"a session on the campus network, China Mobile first", mobile(), "23D001", "@zgyd", true},
		{"a session on China Mobile, the campus network first", campus(), "23D001@zgyd", "", true},
		{"a session already on the first one", mobile(), "23D001@zgyd", "", false},
		{"an entry with one service type, so nothing to move onto", Account{
			Username: "23D001", Password: "p", ISPs: []string{"@zgyd"},
		}, "23D001", "", false},
		{"an entry with no password", Account{
			Username: "23D001", ISPs: []string{"@zgyd", ""},
		}, "23D001", "", false},
		{"an entry that is switched off", Account{
			Username: "23D001", Password: "p", Disabled: true, ISPs: []string{"@zgyd", ""},
		}, "23D001", "", false},
		{"an account the list does not name", mobile(), "stranger", "", false},
		{"no account reported at all", mobile(), "", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{AccountPrefix: ",0,", Accounts: []Account{c.entry}}

			got, ok := cfg.PreferredFor(c.reported)
			if ok != c.moved {
				t.Fatalf("PreferredFor(%q) moved = %v, want %v", c.reported, ok, c.moved)
			}
			if !c.moved {
				return
			}
			if got.Index != 0 || got.Username != c.entry.Username || got.ISP != c.want {
				t.Errorf("PreferredFor(%q) = %+v, want entry 0 as %s", c.reported, got, c.want)
			}
			if got.Password != c.entry.Password {
				t.Errorf("PreferredFor(%q) = %+v, want the entry's own password", c.reported, got)
			}
		})
	}
}

func TestConfiguredNeedsOneEntryThatCanSignIn(t *testing.T) {
	cfg := &Config{}
	if cfg.Configured() {
		t.Error("an empty configuration counts as configured")
	}

	cfg.Accounts = []Account{{Username: "   ", Password: "p@ss", ISPs: []string{""}}}
	if cfg.Configured() {
		t.Error("a blank user name counts as configured")
	}

	cfg.Accounts = []Account{{Username: "s1234567", Password: "", ISPs: []string{""}}}
	if cfg.Configured() {
		t.Error("a missing password counts as configured")
	}

	cfg.Accounts = []Account{{Username: "s1234567", Password: "p@ss", Disabled: true, ISPs: []string{""}}}
	if cfg.Configured() {
		t.Error("an entry that is disabled counts as configured")
	}

	cfg.Accounts = append(cfg.Accounts, Account{Username: "s7654321", Password: "p@ss", ISPs: []string{""}})
	if !cfg.Configured() {
		t.Error("a usable entry does not count as configured")
	}
}

// See KeepPasswordsFrom: an empty password from the page means "the password
// already stored", and it survives a reordering of the list, so the entries are
// matched up by their account.
func TestAnEntryKeepsThePasswordThePageDidNotSend(t *testing.T) {
	stored := &Config{Accounts: []Account{
		{Username: "first", Password: "one", ISPs: []string{""}},
		{Username: "second", Password: "two", ISPs: []string{""}},
		{Username: "third", Password: "three", ISPs: []string{""}},
	}}

	cases := []struct {
		name string
		sent []Account
		want []string
	}{
		{
			"a list nothing was done to",
			[]Account{
				{Username: "first", ISPs: []string{""}},
				{Username: "second", ISPs: []string{""}},
				{Username: "third", ISPs: []string{""}},
			},
			[]string{"one", "two", "three"},
		},
		{
			"a list that was reordered",
			[]Account{
				{Username: "third", ISPs: []string{""}},
				{Username: "first", ISPs: []string{""}},
				{Username: "second", ISPs: []string{""}},
			},
			[]string{"three", "one", "two"},
		},
		{
			"a list an entry was taken out of",
			[]Account{
				{Username: "second", ISPs: []string{""}},
				{Username: "third", ISPs: []string{""}},
			},
			[]string{"two", "three"},
		},
		{
			"a password the reader typed",
			[]Account{
				{Username: "first", Password: "typed", ISPs: []string{""}},
				{Username: "second", ISPs: []string{""}},
			},
			[]string{"typed", "two"},
		},
		{
			"an entry that was added",
			[]Account{
				{Username: "first", ISPs: []string{""}},
				{Username: "newcomer", ISPs: []string{""}},
			},
			[]string{"one", ""},
		},
		{
			"an entry whose account was renamed",
			[]Account{
				{Username: "renamed", ISPs: []string{""}},
				{Username: "second", ISPs: []string{""}},
			},
			[]string{"", "two"},
		},
		{
			"a second entry of an account that is already used",
			[]Account{
				{Username: "first", ISPs: []string{""}},
				{Username: "first", ISPs: []string{""}},
			},
			[]string{"one", ""},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next := &Config{Accounts: append([]Account(nil), c.sent...)}
			next.KeepPasswordsFrom(stored)
			if len(next.Accounts) != len(c.want) {
				t.Fatalf("Accounts = %+v, want %d entries", next.Accounts, len(c.want))
			}
			for i, want := range c.want {
				if got := next.Accounts[i].Password; got != want {
					t.Errorf("entry %d: password = %q, want %q", i, got, want)
				}
			}
		})
	}
}

// The source is what the page is told in place of a password, and the file is
// not a place for it: a file that carried it would be a file that says a secret
// is in it, which the file already shows by carrying the secret.
func TestTheFlagThePageIsToldIsNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{
		Username:       "s1234567",
		Password:       "topsecret",
		PasswordSource: SourceConfig,
		ISPs:           []string{""},
	}}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PasswordSource") || strings.Contains(string(data), "passwordSource") {
		t.Errorf("the flag meant for the page was written:\n%s", data)
	}
}

func TestSaveOmitsThePasswordsWhenTheyAreNotRemembered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{
		{Username: "s1234567", Password: "topsecret", ISPs: []string{""}},
		{Username: "s7654321", Password: "also-secret", ISPs: []string{""}},
	}
	cfg.RememberPassword = false

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"topsecret", "also-secret"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("%s was written although rememberPassword is false", secret)
		}
	}
	var stored struct {
		Accounts []Account `json:"accounts"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("the file must stay valid JSON: %v", err)
	}
	if len(stored.Accounts) != 2 {
		t.Fatalf("the entries themselves have to stay: %+v", stored.Accounts)
	}
	for _, entry := range stored.Accounts {
		if entry.Password != "" {
			t.Errorf("stored password = %q, want an empty string", entry.Password)
		}
	}
	// The configuration in memory keeps them, since the program still has to be
	// able to sign in until it stops.
	if cfg.Accounts[0].Password != "topsecret" {
		t.Error("Save() emptied the password in memory as well")
	}
}

func TestSaveKeepsTheFileToItsOwner(t *testing.T) {
	// The passwords are in the file in clear text, so the file belongs to the
	// user the program runs as alone. Windows has no permission bits of its own:
	// Go reports 0666 for a writable file there whatever the ACL says.
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are a Unix thing")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := fileMode(t, path); got != 0o600 {
		t.Errorf("config.json is %#o, want 0600", got)
	}

	// A file an earlier version left readable by everyone is tightened when it
	// is written again.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := fileMode(t, path); got != 0o600 {
		t.Errorf("a file that was readable by everyone is %#o after Save, want 0600", got)
	}
}

// TestALooseModeIsPutRightAndReported covers the file that is already on disk, a
// file an older version made or a copy a reader moved around: nothing has
// tightened it yet, and the check that runs at startup is what does.
func TestALooseModeIsPutRightAndReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the mode bits are a Unix thing; Windows is given an access list instead")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	report := cfg.Protect()
	if report == "" {
		t.Fatal("a file readable by everyone was not reported")
	}
	if !strings.Contains(report, "0644") {
		t.Errorf("the report does not name the mode it found: %q", report)
	}
	if got := fileMode(t, path); got != 0o600 {
		t.Errorf("the loose file is %#o after Protect, want 0600", got)
	}

	// Nothing left to say once it is right, so a run that finds one of these
	// does not repeat itself on every start.
	if again := cfg.Protect(); again != "" {
		t.Errorf("a file its owner alone can read was reported again: %q", again)
	}
}

// fileMode is the permission bits of a file.
func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestSaveRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{
		{Username: "s1234567", Password: "topsecret", ISPs: []string{"@cucc", "@zgyd"}},
		{Username: "s7654321", Password: "other", Disabled: true, ISPs: []string{""}},
	}
	cfg.CheckIntervalSeconds = 30

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if len(reloaded.Accounts) != 2 {
		t.Fatalf("Accounts = %+v, want both entries", reloaded.Accounts)
	}
	first := reloaded.Accounts[0]
	if first.Username != "s1234567" || first.Password != "topsecret" {
		t.Errorf("the account did not survive: %+v", first)
	}
	// The order of the service types is the order they are tried in, so it has
	// to survive as well.
	if len(first.ISPs) != 2 || first.ISPs[0] != "@cucc" || first.ISPs[1] != "@zgyd" {
		t.Errorf("ISPs = %v, want the order they were saved in", first.ISPs)
	}
	if !reloaded.Accounts[1].Disabled {
		t.Error("the disabled entry came back in the running")
	}
	if reloaded.CheckIntervalSeconds != 30 {
		t.Errorf("CheckIntervalSeconds = %d, want 30", reloaded.CheckIntervalSeconds)
	}
}

func TestSummaryNeverContainsThePassword(t *testing.T) {
	cfg := defaultConfig()
	cfg.Accounts = []Account{
		{Username: "s1234567", Password: "topsecret", ISPs: []string{"@zgyd", ""}},
		{Username: "s7654321", Password: "also-secret", Disabled: true, ISPs: []string{""}},
	}

	summary := cfg.Summary()
	for _, secret := range []string{"topsecret", "also-secret"} {
		if strings.Contains(summary, secret) {
			t.Errorf("the summary leaks a password: %s", summary)
		}
	}
	// It does say which accounts there are, in which order, and whether they are
	// in the running.
	if !strings.Contains(summary, "s1234567") || !strings.Contains(summary, "China Mobile") {
		t.Errorf("the summary does not describe the accounts: %s", summary)
	}
	if !strings.Contains(summary, "off") {
		t.Errorf("the summary does not say that an entry is disabled: %s", summary)
	}
}

// TestCloneSharesNothingWithTheOriginal walks the whole configuration and
// checks that no part of the copy shares storage with the original: a list
// still shared with the running configuration would be written into by a
// refused request. It walks the type, so a list added later counts too.
func TestCloneSharesNothingWithTheOriginal(t *testing.T) {
	cfg := &Config{
		Accounts: []Account{
			{Username: "s1234567", Password: "one", ISPs: []string{"@zgyd", "@cucc"}},
			{Username: "s7654321", Password: "two", Disabled: true, ISPs: []string{""}},
		},
		ProbeURLs:            []string{"http://probe.example/", "http://other.example/"},
		PortalBaseURL:        "http://10.9.1.3/",
		EportalURL:           "http://10.9.1.3:801/eportal/",
		AccountPrefix:        ",0,",
		CheckIntervalSeconds: 15,
		HTTPTimeoutSeconds:   6,
		RememberPassword:     true,
		AutoLogin:            true,
		JsVersion:            "3.3.3",
		Language:             "auto",
	}

	var walk func(orig, dup reflect.Value, path string)
	walk = func(orig, dup reflect.Value, path string) {
		if orig.Type() != dup.Type() {
			t.Errorf("%s: the copy is a %s where the original is a %s", path, dup.Type(), orig.Type())
			return
		}
		switch orig.Kind() {
		// The kinds that hold their value elsewhere, where sharing would show
		// up.
		case reflect.Slice, reflect.Map, reflect.Pointer:
			if !orig.IsNil() && !dup.IsNil() && orig.Pointer() == dup.Pointer() {
				t.Errorf("%s: the copy is the same storage as the original", path)
				return
			}
		}
		switch orig.Kind() {
		case reflect.Slice, reflect.Array:
			if orig.Len() != dup.Len() {
				t.Errorf("%s: the copy holds %d of the %d", path, dup.Len(), orig.Len())
				return
			}
			for i := 0; i < orig.Len(); i++ {
				walk(orig.Index(i), dup.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		case reflect.Map:
			for _, key := range orig.MapKeys() {
				walk(orig.MapIndex(key), dup.MapIndex(key), fmt.Sprintf("%s[%v]", path, key))
			}
		case reflect.Pointer, reflect.Interface:
			if !orig.IsNil() && !dup.IsNil() {
				walk(orig.Elem(), dup.Elem(), path)
			}
		case reflect.Struct:
			for i := 0; i < orig.NumField(); i++ {
				// Unexported fields are not decoded into, and the strings among
				// them share storage as strings do.
				if !orig.Type().Field(i).IsExported() {
					continue
				}
				walk(orig.Field(i), dup.Field(i), path+"."+orig.Type().Field(i).Name)
			}
		}
	}

	clone := cfg.Clone()
	walk(reflect.ValueOf(*cfg), reflect.ValueOf(*clone), "Config")

	// The walk above says the two do not point at the same place; writing
	// through one of them shows it the way the settings page would.
	clone.Accounts[0].Username = "someone-else"
	clone.Accounts[0].ISPs[0] = "@ctc"
	clone.Accounts = append(clone.Accounts, Account{Username: "third"})
	clone.ProbeURLs[0] = "http://changed.example/"

	if cfg.Accounts[0].Username != "s1234567" {
		t.Errorf("writing to the copy changed the original's account: %q", cfg.Accounts[0].Username)
	}
	if cfg.Accounts[0].ISPs[0] != "@zgyd" {
		t.Errorf("writing to the copy changed the original's service types: %v", cfg.Accounts[0].ISPs)
	}
	if len(cfg.Accounts) != 2 {
		t.Errorf("appending to the copy changed the original: %+v", cfg.Accounts)
	}
	if cfg.ProbeURLs[0] != "http://probe.example/" {
		t.Errorf("writing to the copy changed the original's probes: %v", cfg.ProbeURLs)
	}
}

// "Something has been written down" and "there is something to sign in with"
// are two questions. Reading them as one made every start of a configuration
// that stores no password look like a first run.
func TestWrittenDownIsNotTheSameAsAbleToSignIn(t *testing.T) {
	cases := []struct {
		name         string
		cfg          *Config
		wantAccounts bool
		wantUsable   bool
	}{
		{
			name:         "nothing at all",
			cfg:          &Config{Accounts: []Account{}},
			wantAccounts: false,
			wantUsable:   false,
		},
		{
			name:         "the account without the password",
			cfg:          &Config{Accounts: []Account{{Username: "s1234567", ISPs: []string{""}}}},
			wantAccounts: true,
			wantUsable:   false,
		},
		{
			name:         "both",
			cfg:          &Config{Accounts: []Account{{Username: "s1234567", Password: "p", ISPs: []string{""}}}},
			wantAccounts: true,
			wantUsable:   true,
		},
		{
			name:         "an entry with a password and no account",
			cfg:          &Config{Accounts: []Account{{Password: "p", ISPs: []string{""}}}},
			wantAccounts: false,
			wantUsable:   false,
		},
		{
			name:         "an entry that is switched off",
			cfg:          &Config{Accounts: []Account{{Username: "s1234567", Password: "p", Disabled: true, ISPs: []string{""}}}},
			wantAccounts: true,
			wantUsable:   false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.HasAccounts(); got != c.wantAccounts {
				t.Errorf("HasAccounts = %v, want %v", got, c.wantAccounts)
			}
			if got := c.cfg.Configured(); got != c.wantUsable {
				t.Errorf("Configured = %v, want %v", got, c.wantUsable)
			}
		})
	}
}

// The update check is off until the reader turns it on: it is a request to a
// server they did not choose this run, and the channel that cannot surprise
// anyone is what it starts on.
func TestTheUpdateCheckStartsOffOnTheStableChannel(t *testing.T) {
	cfg := defaultConfig()
	if cfg.CheckForUpdates {
		t.Error("CheckForUpdates is on in a configuration that has just been made")
	}
	if cfg.UpdateChannel != UpdateChannelStable {
		t.Errorf("UpdateChannel = %q, want %q", cfg.UpdateChannel, UpdateChannelStable)
	}
}

// Both keys travel through the file and through the settings page, which decodes
// the whole configuration back onto a copy of the running one.
func TestTheUpdateKeysSurviveTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"accounts":[],"checkForUpdates":true,"updateChannel":"prerelease"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.CheckForUpdates {
		t.Error("checkForUpdates did not survive the file")
	}
	if cfg.UpdateChannel != UpdateChannelPrerelease {
		t.Errorf("updateChannel = %q, want %q", cfg.UpdateChannel, UpdateChannelPrerelease)
	}
}

// TestLoadConfigChecksWhatItRead: a file that no page wrote still has to be one this
// program can work with. The settings page checks what the page sends; a file written
// by hand, by an installer or by an older version has no page, so the loading path
// reports it and takes the addresses out of its own hands.
func TestLoadConfigChecksWhatItRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"accounts":[{"username":"s1234567","password":"p@ss","isps":[""]}],` +
		`"portalBaseUrl":"ftp://portal.example/",` +
		`"eportalUrl":"http://10.9.1.3:801/eportal/#",` +
		`"probeUrls":["http://ok.example/","http://bad.example/#x"]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err == nil {
		t.Fatal("a configuration this program cannot work with was accepted")
	}
	for _, want := range []string{"portalBaseUrl", "eportalUrl", "probeUrls[1]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the report does not name %s: %v", want, err)
		}
	}
	// What did not pass is not what this run would hand the passwords to.
	if cfg.PortalBaseURL != defaultConfig().PortalBaseURL || cfg.EportalURL != defaultConfig().EportalURL {
		t.Errorf("the addresses that did not pass were kept: %q %q", cfg.PortalBaseURL, cfg.EportalURL)
	}
	// The file itself is left as it was: a load reports, it does not rewrite.
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != body {
		t.Errorf("the file was rewritten by a load that refused it:\n%s", stored)
	}
}

// TestValidHTTPURLRefusesWhatAnAddressCannotCarry covers the addresses that read like
// a URL but cannot be submitted to: a fragment swallows every parameter that is added
// after it, and whitespace or a control character is not part of an address.
func TestValidHTTPURLRefusesWhatAnAddressCannotCarry(t *testing.T) {
	invalid := []string{
		"http://10.9.1.3:801/eportal/#",
		"http://10.9.1.3:801/eportal/#x",
		"http://10.9.1.3/eportal/ #",
		"http://10.9.1.3/\tportal/",
		"http://10.9.1.3/p\nortal/",
		"http://10.9.1.3/por tal/",
		"http://10.9.1.3/portal\x00/",
		"http://10.9.1.3/portal/\x7f",
	}
	for _, raw := range invalid {
		if ValidHTTPURL(raw) {
			t.Errorf("ValidHTTPURL(%q) = true, want false", raw)
		}
	}
	// What is still an address: the space a hand edited file may carry around the value
	// is taken off before the address is read.
	for _, raw := range []string{"http://10.9.1.3:801/eportal/", "  https://portal.example/eportal/  "} {
		if !ValidHTTPURL(raw) {
			t.Errorf("ValidHTTPURL(%q) = false, want true", raw)
		}
	}
}

// TestSaveRefusesASymbolicLinkInPlaceOfTheConfigFile: the write, the chmod and the
// access list all follow a link, so the passwords would land in whatever it points at.
func TestSaveRefusesASymbolicLinkInPlaceOfTheConfigFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making a symbolic link needs a privilege on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}}

	target := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(target, []byte("do not touch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}

	err = cfg.Save()
	if err == nil {
		t.Fatal("a save through a symbolic link was accepted")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
	// What the link points at is untouched, and so is its mode.
	kept, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != "do not touch\n" {
		t.Errorf("the file behind the link was written: %q", kept)
	}
	if mode := fileMode(t, target); mode != 0o600 {
		t.Errorf("the file behind the link is %04o, want 0600", mode)
	}
}

// TestSaveRefusesADirectoryReachedThroughASymbolicLink: a linked directory is where a
// file written through it lands, and MkdirAll says nothing about it.
func TestSaveRefusesADirectoryReachedThroughASymbolicLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making a symbolic link needs a privilege on Windows")
	}
	base := t.TempDir()
	elsewhere := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "configdir")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(elsewhere, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	through := cfg.Clone()
	through.path = filepath.Join(link, "config.json")
	through.Accounts = []Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}}

	err = through.Save()
	if err == nil {
		t.Fatal("a save through a linked directory was accepted")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
	entries, err := os.ReadDir(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("a refused save left %s in the directory the link points at", e.Name())
		}
	}
	stored, err := os.ReadFile(filepath.Join(elsewhere, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "topsecret") {
		t.Errorf("the passwords were written through the link:\n%s", stored)
	}
}

// TestSaveRefusesADirectoryOthersCanWrite: the mode of the file cannot keep anybody
// with write access to the directory from moving it away and putting their own in its
// place, so such a directory is not one the passwords are written into.
func TestSaveRefusesADirectoryOthersCanWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the mode bits are a Unix thing; Windows is given an access list instead")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{""}}}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	err = cfg.Save()
	if err == nil {
		t.Fatal("a save into a directory everybody can write in was accepted")
	}
	if !strings.Contains(err.Error(), "writable") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "topsecret") {
		t.Errorf("the passwords were written into a directory others can write in:\n%s", stored)
	}
}

// TestASaveThatCannotBeMadeLeavesTheFileThatIsThere covers the failure that comes
// before any write: the configuration on disk stays the one written last, whole and
// loadable, with nothing half written left beside it.
func TestASaveThatCannotBeMadeLeavesTheFileThatIsThere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the mode bits are a Unix thing")
	}
	if os.Geteuid() == 0 {
		t.Skip("a root process writes into a directory whatever its mode says")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", Password: "keep-me", ISPs: []string{""}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	cfg.Accounts[0].Password = "replacement"
	if err := cfg.Save(); err == nil {
		t.Fatal("a save into a directory that cannot be written was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the configuration that was there is gone: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("a save that failed changed the file:\n--- before\n%s\n--- after\n%s", before, after)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("a save that failed left %s behind", e.Name())
		}
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("the configuration that survived a failed save does not load: %v", err)
	}
	if len(reloaded.Accounts) != 1 || reloaded.Accounts[0].Password != "keep-me" {
		t.Errorf("the surviving configuration is not the one that was written: %+v", reloaded.Accounts)
	}
}

// TestASecretGivenToTheRunIsNotWrittenByASaveFromThePage is the promise --password
// makes: the value is for this run, so a save that comes out of the settings page
// leaves it out of the file, while the account keeps working until the program stops.
func TestASecretGivenToTheRunIsNotWrittenByASaveFromThePage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", Password: "run-only", PasswordSource: SourceRun, ISPs: []string{""}}}

	// An ordinary save writes it: that is the path --save takes, and it is the operator
	// asking for the value to be written down.
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), "run-only") {
		t.Fatalf("an ordinary save did not write the password of this run:\n%s", stored)
	}

	// What the page sends back for an entry whose password field was left alone: the
	// account and the service type, and no password.
	next := cfg.PageBase()
	next.Accounts = []Account{{Username: "s1234567", Password: "", ISPs: []string{""}}}
	if notice := next.ReconcilePasswords(cfg); notice != "" {
		t.Fatalf("ReconcilePasswords: %s", notice)
	}
	if next.Accounts[0].Password != "run-only" || next.Accounts[0].PasswordSource != SourceRun {
		t.Errorf("entry = %+v, want the secret of this run kept for this run", next.Accounts[0])
	}
	if err := next.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	stored, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "run-only") {
		t.Errorf("a save from the page wrote the secret that was given to the run:\n%s", stored)
	}
	if next.Accounts[0].Password != "run-only" {
		t.Errorf("Save emptied the password the run still has to sign in with: %+v", next.Accounts[0])
	}
}

// TestAnUnknownUpdateChannelIsNotWidened covers the setting that says which releases
// the program may move to. The file is written by hand as often as by the page, so a
// value this program does not know - a name from a version that is not this one, a
// misspelling, whatever a page was told to send - must not widen what it takes. The
// fallback is the channel that cannot surprise anyone, and the two names are compared
// as they are written: reading "Prerelease" or " prerelease" as the prerelease channel
// would hand the reader releases they did not sign up for.
func TestAnUnknownUpdateChannelIsNotWidened(t *testing.T) {
	cases := []struct {
		name    string
		channel string
		want    update.Channel
	}{
		{"nothing written down", "", update.Stable},
		{"the stable channel", UpdateChannelStable, update.Stable},
		{"the prerelease channel", UpdateChannelPrerelease, update.Prerelease},
		{"a name this program does not know", "beta", update.Stable},
		{"another name this program does not know", "nightly", update.Stable},
		{"a path where a channel belongs", "../etc", update.Stable},
		{"the prerelease channel in another case", "Prerelease", update.Stable},
		{"the prerelease channel in capitals", "PRERELEASE", update.Stable},
		{"the prerelease channel with a space in front of it", " prerelease", update.Stable},
		{"the prerelease channel with a space behind it", "prerelease ", update.Stable},
		{"the prerelease channel with a newline behind it", "prerelease\n", update.Stable},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{UpdateChannel: c.channel}
			got := cfg.UpdateChannelValue()
			if got != c.want {
				t.Fatalf("UpdateChannelValue() with %q = %q, want %q", c.channel, got, c.want)
			}
			if !update.ValidChannel(string(got)) {
				t.Errorf("UpdateChannelValue() with %q = %q, which is not a channel this program knows", c.channel, got)
			}
		})
	}

	// The same question asked of a file, which is the shape a hand-written or an
	// older configuration arrives in.
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"accounts":[],"checkForUpdates":true,"updateChannel":"beta"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.UpdateChannel != "beta" {
		t.Fatalf("updateChannel = %q, so this test is not reading the value the file holds", cfg.UpdateChannel)
	}
	if got := cfg.UpdateChannelValue(); got != update.Stable {
		t.Errorf("a file naming the channel %q reads as %q, want %q", cfg.UpdateChannel, got, update.Stable)
	}
}

// TestPortalNoticeNamesTheAddressThatIsNotThisBuilds covers the line that describes
// where the passwords are submitted. A deployment on another campus is a real one and
// the address is the operator's to set, so this is a warning and not a refusal - but it
// has to be said, because the log the next start writes and the line a save from the
// page writes both carry it, and a change of address is what is looked for afterwards.
func TestPortalNoticeNamesTheAddressThatIsNotThisBuilds(t *testing.T) {
	shipped := defaultConfig()

	cases := []struct {
		name    string
		portal  string
		eportal string
		// want is every piece that has to appear; an empty one means nothing is said.
		want []string
		// absent is every piece that must not appear: the field that did not move.
		absent []string
	}{
		{
			name:    "the address this build was made for",
			portal:  shipped.PortalBaseURL,
			eportal: shipped.EportalURL,
		},
		{
			name:    "the portal home page moved to another campus",
			portal:  "http://portal.other.example/",
			eportal: shipped.EportalURL,
			want:    []string{"portalBaseUrl", `"portal.other.example"`},
			absent:  []string{"eportalUrl"},
		},
		{
			name:    "the eportal moved to another campus",
			portal:  shipped.PortalBaseURL,
			eportal: "http://eportal.other.example:801/eportal/",
			want:    []string{"eportalUrl", `"eportal.other.example:801"`},
			absent:  []string{"portalBaseUrl"},
		},
		{
			name:    "both addresses moved",
			portal:  "http://portal.other.example/",
			eportal: "http://eportal.other.example:801/eportal/",
			want:    []string{"portalBaseUrl", "eportalUrl"},
		},
		{
			// The address is read as host and port together, because that is what a
			// request is sent to and where the passwords are submitted: a portal
			// moved to another port of the same machine is another address, and the
			// line says which one with the port in it.
			name:    "the same host on another port",
			portal:  "http://10.9.1.3:8443/",
			eportal: shipped.EportalURL,
			want:    []string{"portalBaseUrl", `"10.9.1.3:8443"`},
			absent:  []string{"eportalUrl"},
		},
		{
			// An address that cannot be read as one is not the address this build
			// was made for either, so it is reported with nothing where the host
			// would be rather than passed over as unchanged.
			name:    "an address that cannot be parsed at all",
			portal:  "http://\x7f/",
			eportal: shipped.EportalURL,
			want:    []string{"portalBaseUrl", `(host "")`},
			absent:  []string{"eportalUrl"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{PortalBaseURL: c.portal, EportalURL: c.eportal}
			notice := cfg.PortalNotice()
			if len(c.want) == 0 && notice != "" {
				t.Fatalf("PortalNotice() = %q, want nothing said about the address this build ships with", notice)
			}
			for _, want := range c.want {
				if !strings.Contains(notice, want) {
					t.Errorf("PortalNotice() = %q, want it to name %s", notice, want)
				}
			}
			for _, absent := range c.absent {
				if strings.Contains(notice, absent) {
					t.Errorf("PortalNotice() = %q, which names %s although that address did not move", notice, absent)
				}
			}
		})
	}
}

// TestTheSummaryOfAChangedAddressCarriesTheNotice covers where the notice has to reach:
// every line that describes the configuration carries it, so the log the next start
// writes records the address the passwords went to without one host being hard coded as
// the only one allowed.
func TestTheSummaryOfAChangedAddressCarriesTheNotice(t *testing.T) {
	cfg := defaultConfig()
	cfg.Accounts = []Account{{Username: "s1234567", Password: "topsecret", ISPs: []string{"@zgyd"}}}

	summary := cfg.Summary()
	if strings.Contains(summary, "portalBaseUrl") {
		t.Errorf("the summary of the address this build ships with carries a notice: %q", summary)
	}
	if !strings.Contains(summary, "http://10.9.1.3/") {
		t.Errorf("the summary does not carry the address in use: %q", summary)
	}

	cfg.PortalBaseURL = "http://portal.other.example/"
	summary = cfg.Summary()
	if !strings.Contains(summary, "portalBaseUrl") {
		t.Errorf("the summary of a changed address does not say so: %q", summary)
	}
	if !strings.Contains(summary, "portal.other.example") {
		t.Errorf("the notice does not name the address the passwords go to: %q", summary)
	}
	if !strings.Contains(summary, "http://portal.other.example/") {
		t.Errorf("the summary lost the address itself: %q", summary)
	}
	if strings.Contains(summary, "topsecret") {
		t.Error("the line the notice is added to carries the password")
	}
}

// TestSwitchToMovesTheSessionOntoAnotherServiceType covers what the settings page asks
// when the reader picks a service type for the session that is up. A sign-in drops the
// session that is working, so nothing is attempted for nothing: a session already on the
// service type asked for, an account the list does not name, an entry that is switched
// off or cannot sign in, and a service type the entry does not list all answer that
// there is no attempt, and the answer is empty rather than a half filled one.
func TestSwitchToMovesTheSessionOntoAnotherServiceType(t *testing.T) {
	accounts := []Account{
		{Username: "23D001", Password: "p@ss", ISPs: []string{"", "@zgyd"}},
		{Username: "off", Password: "p@ss", Disabled: true, ISPs: []string{"@zgyd", "@cucc"}},
		{Username: "nopass", ISPs: []string{"@zgyd", "@cucc"}},
	}
	cfg := &Config{AccountPrefix: ",0,", Accounts: accounts}

	cases := []struct {
		name     string
		reported string
		isp      string
		moved    bool
		index    int
	}{
		{"the campus network moved onto China Mobile", "23D001", "@zgyd", true, 0},
		{"a session on China Mobile moved back onto the campus network", ",0,23D001@zgyd", "", true, 0},
		{"a session already on the service type asked for", ",0,23D001@zgyd", "@zgyd", false, 0},
		{"a service type the entry does not list", ",0,23D001@zgyd", "@cucc", false, 0},
		{"a service type no entry lists", ",0,23D001@zgyd", "@ctc", false, 0},
		{"an account the list does not name", ",0,stranger@zgyd", "@cucc", false, 0},
		{"no account reported at all", "", "@zgyd", false, 0},
		{"an entry that is switched off", ",0,off@zgyd", "@cucc", false, 1},
		{"an entry that cannot sign in", ",0,nopass@zgyd", "@cucc", false, 2},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := cfg.SwitchTo(c.reported, c.isp)
			if ok != c.moved {
				t.Fatalf("SwitchTo(%q, %q) moved = %v (got %+v), want %v", c.reported, c.isp, ok, got, c.moved)
			}
			if !c.moved {
				if got != (Attempt{}) {
					t.Errorf("SwitchTo(%q, %q) answered no attempt and still returned %+v", c.reported, c.isp, got)
				}
				return
			}
			if got.Index != c.index || got.ISP != c.isp {
				t.Errorf("SwitchTo(%q, %q) = %+v, want entry %d submitted as %q", c.reported, c.isp, got, c.index, c.isp)
			}
			if want := accounts[c.index]; got.Username != want.Username || got.Password != want.Password {
				t.Errorf("SwitchTo(%q, %q) = %+v, want the entry's own account and password", c.reported, c.isp, got)
			}
		})
	}
}

// TestEntryLabelNamesTheEntryOrNothing covers the name an entry and a service type are
// written as, in the log and in the interface. Both the page and the log ask for it by
// position, so a position that names no entry answers with nothing instead of reading
// past the end of the list.
func TestEntryLabelNamesTheEntryOrNothing(t *testing.T) {
	cfg := &Config{Accounts: []Account{
		{Username: "23D001"},
		{Username: "s7654321"},
	}}

	cases := []struct {
		name  string
		index int
		isp   string
		want  string
	}{
		{"an entry and a service type of the dropdown", 0, "@zgyd", "23D001 (China Mobile)"},
		{"an entry and the campus network", 0, "", "23D001 (Campus network)"},
		{"the second entry", 1, "@cucc", "s7654321 (China Unicom)"},
		{"a service type the dropdown does not name", 0, "@xyz", "23D001 (custom(@xyz))"},
		{"a position before the first entry", -1, "@zgyd", ""},
		{"a position past the last entry", 2, "@zgyd", ""},
		{"a position far past the last entry", 99, "@zgyd", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cfg.EntryLabel(c.index, c.isp); got != c.want {
				t.Errorf("EntryLabel(%d, %q) = %q, want %q", c.index, c.isp, got, c.want)
			}
		})
	}

	empty := &Config{}
	if got := empty.EntryLabel(0, "@zgyd"); got != "" {
		t.Errorf("EntryLabel(0, ...) on a configuration with no entries = %q, want nothing", got)
	}
}

// TestValidatePointsAtThePasswordFile covers what the page is told about an entry that
// has no password while a password file is named: the password is not missing, it is not
// in that file, and renaming an account is the way that happens. Saying the password is
// required instead would send the reader to a field the page has closed.
func TestValidatePointsAtThePasswordFile(t *testing.T) {
	cfg := &Config{
		Accounts:             []Account{{Username: "s1234567", ISPs: []string{""}}},
		CheckIntervalSeconds: 15,
		HTTPTimeoutSeconds:   6,
	}
	if got := cfg.Validate()["accounts.0.password"]; got != "err.passwordRequired" {
		t.Errorf("an entry with no password and no password file is reported as %q, want %q", got, "err.passwordRequired")
	}

	cfg.PasswordFile = filepath.Join(t.TempDir(), "passwords")
	if got := cfg.Validate()["accounts.0.password"]; got != "err.passwordNotInFile" {
		t.Errorf("an entry with no password and a password file named is reported as %q, want %q", got, "err.passwordNotInFile")
	}
}

// TestAFileNamingAnAccountPrefixThisProgramCannotSend covers the prefix put in front of
// an account before it is submitted. The account is sent as one value, so a space, a
// control character or a character that changes the shape of the address is refused
// here: & starts another parameter, ? and = cut the value in two, and # and % are read
// by the decoder as something other than themselves. A file has no page to refuse these,
// so loading one reports the field and keeps the prefix this build ships with rather
// than submitting something whose shape nobody can see.
func TestAFileNamingAnAccountPrefixThisProgramCannotSend(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		// reported is whether loading the file is expected to complain about the field.
		reported bool
		// want is what the prefix reads afterwards: the one in the file when it can be
		// sent, the one this build ships with when it cannot or was left out.
		want string
	}{
		{"the prefix this build ships with", ",0,", false, ",0,"},
		{"the prefix left out of the file", "", false, ",0,"},
		{"a prefix with nothing but spaces in it", "   ", false, ",0,"},
		{"a prefix with a parameter separator in it", ",0,&", true, ",0,"},
		{"a prefix with a space in it", " ,0,", true, ",0,"},
		{"a prefix with a tab in it", "\t,0,", true, ",0,"},
		{"a prefix with a control character in it", ",0,\u007f", true, ",0,"},
		{"a prefix with a percent sign in it", ",0,%2C", true, ",0,"},
		{"a prefix with a sharp in it", ",0,#", true, ",0,"},
		{"a prefix with a question mark in it", "a?b", true, ",0,"},
		{"a prefix with an equals sign in it", "a=b", true, ",0,"},
		{"a prefix with a slash in it", ",0,/", true, ",0,"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			prefix, err := json.Marshal(c.prefix)
			if err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(
				`{"accountPrefix":%s,"accounts":[{"username":"s1234567","password":"p@ss","isps":[""]}]}`,
				prefix)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, err := LoadConfig(path)
			if c.reported {
				if err == nil {
					t.Errorf("a file naming the account prefix %q was accepted", c.prefix)
				} else if !strings.Contains(err.Error(), "accountPrefix") {
					t.Errorf("the report does not name the field: %v", err)
				}
			} else if err != nil {
				t.Fatalf("a prefix this program can send was refused: %v", err)
			}
			if cfg == nil {
				t.Fatal("no configuration came back for a file that could not be used")
			}
			if cfg.AccountPrefix != c.want {
				t.Errorf("accountPrefix reads %q after loading %q, want %q", cfg.AccountPrefix, c.prefix, c.want)
			}
		})
	}
}
