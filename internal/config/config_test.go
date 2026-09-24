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

	// Saving it again writes the list and drops the fields that are no longer
	// read, so a file is never half in one shape and half in the other.
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

// TestAnOlderFileSignsInAsItDidBefore pins the upgrade path. A file written by
// a version that kept one account has to yield the same user_account and the
// same verdict on whether it can sign in, since an upgrade that changed either
// would leave a working installation signing in as the wrong account, or as
// none at all.
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

	// Saving is what rewrites it: the file is then in one shape rather than half
	// in each.
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
		t.Error("Accounts must be an empty list rather than nothing, so the page can render it")
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
	// before saving rather than reported.
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
		// cannot sign in at all.
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
// bare account is therefore a session signed in without a suffix, which is the
// campus network itself, and naming the entry's first service type instead would
// put another operator's name on a session that is not on it.
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
// that entry. Nothing is named when there is nothing to move onto, which is the
// answer that keeps a working session from being signed in again for no reason.
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
// already stored", and it has to survive a reordering of the list, which is why
// the entries are matched up by their account rather than by their position.
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

// HasPassword is what the page is told in place of a password, and the file is
// not a place for it: a file that carried it would be a file that says a secret
// is in it, which the file already shows by carrying the secret.
func TestTheFlagThePageIsToldIsNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{
		Username:    "s1234567",
		Password:    "topsecret",
		HasPassword: true,
		ISPs:        []string{""},
	}}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "HasPassword") || strings.Contains(string(data), "hasPassword") {
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

// TestCloneSharesNothingWithTheOriginal walks the whole configuration, at every
// level of nesting, and checks that no field of the copy is the storage of the
// field it was copied from. A settings request is decoded onto the copy, so a
// list that is still the running configuration's would be written into it by a
// request that is then refused.
//
// It walks the type rather than naming today's fields, because a list added
// later has to be copied as well and naming the fields would not catch it.
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
