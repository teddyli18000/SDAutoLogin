package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	if !cfg.AutoLogin || !cfg.CheckInternet || !cfg.VerboseLog || !cfg.RememberPassword {
		t.Errorf("every boolean default is meant to be on: %+v", cfg)
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
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"username":"23D001","password":"p@ss"}`)...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a UTF-8 BOM must not stop the parse: %v", err)
	}
	if cfg.Username != "23D001" || cfg.Password != "p@ss" {
		t.Errorf("the file was not read: %+v", cfg)
	}
	// Fields the file leaves out keep their default, which is what makes an
	// older file survive a new release.
	if cfg.CheckIntervalSeconds != 15 {
		t.Errorf("CheckIntervalSeconds = %d, want the default 15", cfg.CheckIntervalSeconds)
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
}

func TestValidateReportsEveryProblem(t *testing.T) {
	errs := (&Config{}).Validate()

	for _, field := range []string{
		"username", "password", "checkIntervalSeconds",
		"httpTimeoutSeconds", "portalBaseUrl", "eportalUrl",
	} {
		if errs[field] == "" {
			t.Errorf("Validate() did not report %s, got %v", field, errs)
		}
	}
}

func TestValidateAcceptsTheDefaults(t *testing.T) {
	cfg := defaultConfig()
	cfg.Username = "23D001"
	cfg.Password = "p@ss"

	if errs := cfg.Validate(); len(errs) != 0 {
		t.Errorf("Validate() = %v, want no errors", errs)
	}
}

func TestValidateSkipsBlankProbesAndReportsTheBadOnes(t *testing.T) {
	cfg := defaultConfig()
	cfg.Username = "23D001"
	cfg.Password = "p@ss"
	// A blank entry is what an empty row in the settings page looks like, and it
	// is dropped before saving rather than reported.
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

func TestAccountAndOperatorName(t *testing.T) {
	cfg := &Config{Username: "23D001", AccountPrefix: ",0,", AccountSuffix: "@zgyd"}

	if got := cfg.Account(); got != ",0,23D001@zgyd" {
		t.Errorf("Account() = %q", got)
	}
	if got := cfg.OperatorName(); got != "China Mobile" {
		t.Errorf("OperatorName() = %q, want the operator the suffix belongs to", got)
	}

	cfg.AccountSuffix = "@unknown"
	if got := cfg.OperatorName(); got != "custom(@unknown)" {
		t.Errorf("OperatorName() = %q, want a custom suffix to be shown as is", got)
	}
}

func TestConfiguredNeedsBothAnAccountAndAPassword(t *testing.T) {
	cfg := &Config{}
	if cfg.Configured() {
		t.Error("an empty configuration counts as configured")
	}

	cfg.Username = "   "
	cfg.Password = "p@ss"
	if cfg.Configured() {
		t.Error("a blank user name counts as configured")
	}

	cfg.Username = "23D001"
	cfg.Password = ""
	if cfg.Configured() {
		t.Error("a missing password counts as configured")
	}

	cfg.Password = "p@ss"
	if !cfg.Configured() {
		t.Error("a complete account does not count as configured")
	}
}

func TestSaveOmitsThePasswordWhenItIsNotRemembered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Username = "23D001"
	cfg.Password = "topsecret"
	cfg.RememberPassword = false

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "topsecret") {
		t.Error("the password was written although rememberPassword is false")
	}
	var stored map[string]any
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("the file must stay valid JSON: %v", err)
	}
	if stored["password"] != "" {
		t.Errorf("stored password = %v, want an empty string", stored["password"])
	}
}

func TestSaveRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Username = "23D001"
	cfg.Password = "topsecret"
	cfg.AccountSuffix = "@cucc"
	cfg.CheckIntervalSeconds = 30

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if reloaded.Username != cfg.Username || reloaded.Password != cfg.Password {
		t.Errorf("account did not survive: %+v", reloaded)
	}
	if reloaded.AccountSuffix != "@cucc" || reloaded.CheckIntervalSeconds != 30 {
		t.Errorf("settings did not survive: %+v", reloaded)
	}
}

func TestSummaryNeverContainsThePassword(t *testing.T) {
	cfg := defaultConfig()
	cfg.Username = "23D001"
	cfg.Password = "topsecret"

	if summary := cfg.Summary(); strings.Contains(summary, "topsecret") {
		t.Errorf("the summary leaks the password: %s", summary)
	}
}
