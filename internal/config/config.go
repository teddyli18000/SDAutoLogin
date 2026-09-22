// Package config reads and writes config.json and holds the settings type
// every other package works with.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"sdautologin/internal/i18n"
)

// Config mirrors config.json, stored next to the executable.
type Config struct {
	// Username accepts a student id, a short name or a full mail address.
	Username string `json:"username"`
	Password string `json:"password"`
	// RememberPassword decides whether Password is written to disk.
	RememberPassword bool `json:"rememberPassword"`

	// PortalBaseURL is the portal home page, which embeds the client IP the
	// portal sees (the NAT egress address, not the local one).
	PortalBaseURL string `json:"portalBaseUrl"`
	// EportalURL is the Dr.COM eportal root, note the port 801.
	EportalURL string `json:"eportalUrl"`

	// AccountPrefix is fixed to ",0," for a desktop browser login.
	AccountPrefix string `json:"accountPrefix"`
	// AccountSuffix selects the ISP, see Operators.
	AccountSuffix string `json:"accountSuffix"`

	CheckIntervalSeconds int  `json:"checkIntervalSeconds"`
	HTTPTimeoutSeconds   int  `json:"httpTimeoutSeconds"`
	AutoLogin            bool `json:"autoLogin"`
	CheckInternet        bool `json:"checkInternet"`
	// VerboseLog writes per-check diagnostics, which is what makes a dropped
	// session diagnosable after the fact.
	VerboseLog bool `json:"verboseLog"`
	// LogToFile writes the log to logs/app.log as well as to the console. It is
	// the switch a machine with a small flash turns off, since what the program
	// writes to the console is already collected by its service manager, and the
	// rotating file is the same text on the flash a second time.
	LogToFile bool     `json:"logToFile"`
	ProbeURLs []string `json:"probeUrls"`
	JsVersion string   `json:"jsVersion"`
	// Language selects the interface language: auto, zh or en.
	Language string `json:"language"`

	path string
}

func defaultConfig() *Config {
	return &Config{
		PortalBaseURL:        "http://10.9.1.3/",
		EportalURL:           "http://10.9.1.3:801/eportal/",
		AccountPrefix:        ",0,",
		CheckIntervalSeconds: 15,
		HTTPTimeoutSeconds:   6,
		AutoLogin:            true,
		RememberPassword:     true,
		CheckInternet:        true,
		VerboseLog:           true,
		LogToFile:            true,
		Language:             string(i18n.LangAuto),
		ProbeURLs: []string{
			"http://connect.rom.miui.com/generate_204",
			"http://www.baidu.com",
			"http://www.msftconnecttest.com/connecttest.txt",
		},
		JsVersion: "3.3.3",
	}
}

// LoadConfig reads path, creating it with defaults when missing.
func LoadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	cfg.path = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if e := cfg.Save(); e != nil {
				return cfg, fmt.Errorf("write default config: %w", e)
			}
			return cfg, nil
		}
		return cfg, err
	}
	// Several Windows editors prepend a UTF-8 BOM, which json rejects.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	// Unmarshal onto the defaults so fields added later keep sane values.
	if err := json.Unmarshal(data, cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.normalize()
	return cfg, nil
}

func (c *Config) Path() string { return c.path }

func (c *Config) normalize() {
	if c.CheckIntervalSeconds < 5 {
		c.CheckIntervalSeconds = 5
	}
	if c.HTTPTimeoutSeconds < 2 {
		c.HTTPTimeoutSeconds = 2
	}
	if strings.TrimSpace(c.PortalBaseURL) == "" {
		c.PortalBaseURL = defaultConfig().PortalBaseURL
	}
	if strings.TrimSpace(c.EportalURL) == "" {
		c.EportalURL = defaultConfig().EportalURL
	}
	if strings.TrimSpace(c.JsVersion) == "" {
		c.JsVersion = defaultConfig().JsVersion
	}
	switch i18n.Lang(c.Language) {
	case i18n.LangZH, i18n.LangEN:
	default:
		c.Language = string(i18n.LangAuto)
	}
}

// Save writes config.json, omitting the password when it is not remembered.
func (c *Config) Save() error {
	c.normalize()
	data, err := c.marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, append(data, '\n'), 0o644)
}

func (c *Config) marshal() ([]byte, error) {
	type persistConfig Config
	v := persistConfig(*c)
	if !c.RememberPassword {
		v.Password = ""
	}
	return json.MarshalIndent(v, "", "  ")
}

// Operator is one ISP entry of the login page dropdown.
type Operator struct {
	Name   string
	Suffix string
}

// Operators mirrors the ISP_select options the portal login page serves.
var Operators = []Operator{
	{"Campus network", ""},
	{"China Mobile", "@zgyd"},
	{"China Unicom", "@cucc"},
	{"China Telecom", "@ctc"},
}

func (c *Config) OperatorName() string {
	for _, op := range Operators {
		if op.Suffix == c.AccountSuffix {
			return op.Name
		}
	}
	return "custom(" + c.AccountSuffix + ")"
}

func (c *Config) Configured() bool {
	return strings.TrimSpace(c.Username) != "" && c.Password != ""
}

// Account is the value submitted as user_account.
func (c *Config) Account() string {
	return c.AccountPrefix + c.Username + c.AccountSuffix
}

// Summary describes the active settings for the log. The password is never
// included, so this is safe to print.
func (c *Config) Summary() string {
	return fmt.Sprintf(
		"account=%q isp=%q prefix=%q suffix=%q interval=%ds timeout=%ds autoLogin=%v checkInternet=%v verboseLog=%v logToFile=%v rememberPassword=%v language=%q portal=%s eportal=%s",
		c.Username, c.OperatorName(), c.AccountPrefix, c.AccountSuffix,
		c.CheckIntervalSeconds, c.HTTPTimeoutSeconds, c.AutoLogin, c.CheckInternet,
		c.VerboseLog, c.LogToFile, c.RememberPassword, c.Language, c.PortalBaseURL, c.EportalURL)
}

// Validate returns a field name to message key map, where the keys are looked
// up in the shared string table by the settings page.
func (c *Config) Validate() map[string]string {
	errs := map[string]string{}
	if strings.TrimSpace(c.Username) == "" {
		errs["username"] = "err.accountRequired"
	}
	if c.Password == "" {
		errs["password"] = "err.passwordRequired"
	}
	if c.CheckIntervalSeconds < 5 {
		errs["checkIntervalSeconds"] = "err.intervalTooSmall"
	}
	if c.HTTPTimeoutSeconds < 2 {
		errs["httpTimeoutSeconds"] = "err.timeoutTooSmall"
	}
	if !ValidHTTPURL(c.PortalBaseURL) {
		errs["portalBaseUrl"] = "err.urlInvalid"
	}
	if !ValidHTTPURL(c.EportalURL) {
		errs["eportalUrl"] = "err.urlInvalid"
	}
	for i, probe := range c.ProbeURLs {
		if strings.TrimSpace(probe) == "" {
			continue
		}
		if !ValidHTTPURL(probe) {
			errs["probeUrls."+strconv.Itoa(i)] = "err.probeInvalid"
		}
	}
	return errs
}

// ValidHTTPURL reports whether raw is an absolute http or https address.
func ValidHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// CompactProbes drops blank entries and trims the rest before saving.
func CompactProbes(list []string) []string {
	out := make([]string, 0, len(list))
	for _, probe := range list {
		if v := strings.TrimSpace(probe); v != "" {
			out = append(out, v)
		}
	}
	return out
}
