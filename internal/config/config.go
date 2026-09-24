// Package config reads and writes config.json and holds the settings type
// every other package works with.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"sdautologin/internal/i18n"
)

// Account is one entry of the login list: an account, and the service types it
// may be used with, in the order they are to be tried.
//
// The portal allows one session per client address, so these accounts can never
// be online together. The list is therefore a failover order rather than a set
// of sessions running side by side: an account that is out of credit, whose
// password changed or that was closed is skipped for the next one.
type Account struct {
	Username string `json:"username"`
	// Password is empty in the file when the password is not remembered, and it
	// is empty in what the settings page is given: the page writes this field and
	// never reads it, so a secret that is stored is never sent to a browser.
	Password string `json:"password,omitempty"`
	// HasPassword is not part of the file. It is what the page is told instead of
	// the password, so that it can say whether one is stored without being handed
	// one, and it is cleared on the way to disk.
	HasPassword bool `json:"hasPassword,omitempty"`
	// Disabled keeps an entry out of the login list without deleting its
	// password. The field is inverted, so an absent one means "in use": a flag
	// that has to be present for an entry to work is easy to lose while editing
	// by hand.
	Disabled bool `json:"disabled,omitempty"`
	// ISPs are the service suffixes to try for this account, in order, where an
	// empty string is the campus network itself. The suffix is what the portal's
	// own login page appends to the account.
	ISPs []string `json:"isps"`
}

// Config mirrors config.json, stored next to the executable.
type Config struct {
	// Accounts is the login list, in priority order: the first entry that signs
	// in is the one the session keeps.
	Accounts []Account `json:"accounts"`

	// Username, Password and AccountSuffix belong to the versions before the
	// login list existed, when there was one account. They are read, moved into
	// Accounts and not written again.
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	AccountSuffix string `json:"accountSuffix,omitempty"`

	// RememberPassword decides whether the passwords are written to disk. It is
	// one switch for the whole list: a switch per entry would be a row of the
	// same checkbox, and the mode the program is for needs them all on.
	RememberPassword bool `json:"rememberPassword"`

	// PortalBaseURL is the portal home page, which embeds the client IP the
	// portal sees (the NAT egress address, not the local one).
	PortalBaseURL string `json:"portalBaseUrl"`
	// EportalURL is the Dr.COM eportal root, note the port 801.
	EportalURL string `json:"eportalUrl"`

	// AccountPrefix is fixed to ",0," for a desktop browser login.
	AccountPrefix string `json:"accountPrefix"`

	CheckIntervalSeconds int  `json:"checkIntervalSeconds"`
	HTTPTimeoutSeconds   int  `json:"httpTimeoutSeconds"`
	AutoLogin            bool `json:"autoLogin"`
	CheckInternet        bool `json:"checkInternet"`
	// VerboseLog writes per-check diagnostics, so a dropped session stays
	// diagnosable after the fact.
	VerboseLog bool `json:"verboseLog"`
	// LogToFile writes the log to logs/app.log as well as to the console. A
	// machine with a small flash turns it off: its service manager already
	// collects the console output, and the rotating file is the same text on the
	// flash again.
	LogToFile bool     `json:"logToFile"`
	ProbeURLs []string `json:"probeUrls"`
	JsVersion string   `json:"jsVersion"`
	// Language selects the interface language: auto, zh or en.
	Language string `json:"language"`

	path string
}

func defaultConfig() *Config {
	return &Config{
		Accounts:             []Account{},
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

// Clone returns a configuration that shares nothing mutable with c. A settings
// request is decoded onto the clone, so a request refused for being invalid
// leaves the running configuration untouched. A plain struct copy would not do:
// its lists would still be the same arrays, which the JSON decoder fills in
// place.
func (c *Config) Clone() *Config {
	out := *c
	out.Accounts = slices.Clone(c.Accounts)
	for i := range out.Accounts {
		out.Accounts[i].ISPs = slices.Clone(c.Accounts[i].ISPs)
	}
	out.ProbeURLs = slices.Clone(c.ProbeURLs)
	return &out
}

func (c *Config) normalize() {
	c.migrate()
	if c.Accounts == nil {
		c.Accounts = []Account{}
	}
	for i := range c.Accounts {
		c.Accounts[i].normalize()
	}

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

// migrate moves the single account of a configuration file written before the
// login list existed into that list. The old fields are cleared either way, so
// a file that is saved again carries the list alone.
func (c *Config) migrate() {
	legacy := strings.TrimSpace(c.Username) != ""
	if len(c.Accounts) == 0 && legacy {
		c.Accounts = []Account{{
			Username: c.Username,
			Password: c.Password,
			ISPs:     []string{c.AccountSuffix},
		}}
	}
	c.Username, c.Password, c.AccountSuffix = "", "", ""
}

// normalize trims an entry and gives it a service type: an entry that names none
// is the campus network itself, the portal dropdown's first choice.
func (a *Account) normalize() {
	a.Username = strings.TrimSpace(a.Username)
	a.ISPs = CompactSuffixes(a.ISPs)
	if len(a.ISPs) == 0 {
		a.ISPs = []string{""}
	}
}

// usable reports whether this entry can sign in at all.
func (a Account) usable() bool {
	return strings.TrimSpace(a.Username) != "" && a.Password != ""
}

// Attempt is one sign-in attempt: an account and one of the service types it may
// be used with. Config.Attempts flattens the login list into them, in the order
// they are to be tried.
type Attempt struct {
	// Index is the position of the entry in Accounts, so the settings page and
	// the log can point at it.
	Index    int
	Username string
	Password string
	ISP      string
}

// Key identifies the attempt across a restart of the check loop, so the cooldown
// of a rejected entry outlives it.
func (a Attempt) Key() string { return a.Username + "\x00" + a.ISP }

// Entry names the attempt for the log and the interface.
func (a Attempt) Entry() string {
	return fmt.Sprintf("%s (%s)", a.Username, OperatorName(a.ISP))
}

// Attempts is every sign-in attempt the configuration asks for, in priority
// order: the entries that are not disabled and hold both an account and a
// password, each with every service type it may be used with.
func (c *Config) Attempts() []Attempt {
	var out []Attempt
	for i, a := range c.Accounts {
		if a.Disabled || !a.usable() {
			continue
		}
		for _, isp := range a.ISPs {
			out = append(out, Attempt{
				Index:    i,
				Username: a.Username,
				Password: a.Password,
				ISP:      isp,
			})
		}
	}
	return out
}

// Account is the value submitted as user_account for one service type.
func (c *Config) Account(username, isp string) string {
	return c.AccountPrefix + username + isp
}

// Match finds the entry an account string from the portal belongs to, and which
// of its service types that is. It returns -1 when the string names none of the
// configured entries, as happens with a session signed in by something else.
func (c *Config) Match(reported string) (int, string) {
	stripped := strings.TrimPrefix(strings.TrimSpace(reported), c.AccountPrefix)
	if stripped == "" {
		return -1, ""
	}
	for i, a := range c.Accounts {
		for _, isp := range a.ISPs {
			if strings.EqualFold(a.Username+isp, stripped) {
				return i, isp
			}
		}
		// The portal does not always echo the suffix, so the account on its own
		// is taken as a match as well.
		if strings.EqualFold(a.Username, stripped) {
			return i, a.ISPs[0]
		}
	}
	return -1, ""
}

// EntryLabel names one entry of the login list and one of its service types, for
// the log and the interface.
func (c *Config) EntryLabel(index int, isp string) string {
	if index < 0 || index >= len(c.Accounts) {
		return ""
	}
	return fmt.Sprintf("%s (%s)", c.Accounts[index].Username, OperatorName(isp))
}

// Configured reports whether there is anything to sign in with.
func (c *Config) Configured() bool { return len(c.Attempts()) > 0 }

// configFileMode is the mode config.json is written with. The file holds the
// passwords in clear text when they are remembered, so it belongs to its owner
// alone: the program runs as that user, and campus accounts are not shared on a
// machine several people use.
const configFileMode = 0o600

// Save writes config.json, omitting the passwords when they are not remembered.
func (c *Config) Save() error {
	c.normalize()
	data, err := c.marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.path, append(data, '\n'), configFileMode); err != nil {
		return err
	}
	// WriteFile only applies the mode when it creates the file, so a file an
	// earlier version left readable by everyone is tightened here as well. Where
	// the mode bits do nothing, which is Windows, the file is given an access
	// list of its own instead.
	if err := os.Chmod(c.path, configFileMode); err != nil {
		return err
	}
	return restrictToOwner(c.path)
}

// Protect checks that the file holding the passwords cannot be read by anyone
// but its owner and puts it right where it can, returning what it had to say
// about it for the log, or the empty string when there was nothing to say.
//
// Save does the same to the file it writes; this is for the file that is
// already there when the program starts, which an older version or a reader who
// copied it around may have left loose.
func (c *Config) Protect() string { return protect(c.path) }

func (c *Config) marshal() ([]byte, error) {
	type persistConfig Config
	v := persistConfig(*c)
	// The list is always copied: HasPassword is what the page is told and never
	// what the file holds, and the passwords are dropped from it as well when
	// they are not remembered.
	v.Accounts = make([]Account, len(c.Accounts))
	copy(v.Accounts, c.Accounts)
	for i := range v.Accounts {
		v.Accounts[i].HasPassword = false
		if !c.RememberPassword {
			v.Accounts[i].Password = ""
		}
	}
	return json.MarshalIndent(v, "", "  ")
}

// KeepPasswordsFrom fills in the passwords an in-memory configuration was handed
// without, taking them from the one it was built from.
//
// The settings page is never given a stored password, so an entry that comes back
// with that field empty means "the password already there": the page leaves it
// empty for every entry the reader did not type into. An entry with no
// counterpart in the stored list — one just added, or one whose account was
// renamed — has nothing to inherit and stays empty, which the validation then
// reports as a missing password. Nothing here is reached by an entry that carried
// a password of its own: what the reader typed always wins.
func (c *Config) KeepPasswordsFrom(stored *Config) {
	if stored == nil {
		return
	}
	inherited := make([]bool, len(stored.Accounts))
	for i := range c.Accounts {
		if c.Accounts[i].Password != "" {
			continue
		}
		// The entry in the same place, when it is the same account: the case of a
		// list that nothing but an unrelated setting was done to.
		if i < len(stored.Accounts) && !inherited[i] && sameAccount(stored.Accounts[i], c.Accounts[i]) {
			inherited[i] = true
			c.Accounts[i].Password = stored.Accounts[i].Password
			continue
		}
		// Otherwise the entry the account came from, wherever it has moved to.
		for j := range stored.Accounts {
			if inherited[j] || !sameAccount(stored.Accounts[j], c.Accounts[i]) {
				continue
			}
			inherited[j] = true
			c.Accounts[i].Password = stored.Accounts[j].Password
			break
		}
	}
}

// sameAccount reports whether two entries describe the same account. The name is
// what a password belongs to, since the list may be reordered; the comparison is
// on the trimmed name, as an entry is trimmed when it is stored.
func sameAccount(a, b Account) bool {
	left, right := strings.TrimSpace(a.Username), strings.TrimSpace(b.Username)
	return left != "" && left == right
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

// OperatorName names the service type a suffix selects. A suffix the portal's
// dropdown does not list is shown as it is, since the entry is still usable.
func OperatorName(suffix string) string {
	for _, op := range Operators {
		if op.Suffix == suffix {
			return op.Name
		}
	}
	return "custom(" + suffix + ")"
}

// Summary describes the configured accounts and the settings that go with them,
// for the log. No password is included, so this is safe to print.
func (c *Config) Summary() string {
	entries := make([]string, 0, len(c.Accounts))
	for _, a := range c.Accounts {
		services := make([]string, 0, len(a.ISPs))
		for _, isp := range a.ISPs {
			services = append(services, OperatorName(isp))
		}
		state := "on"
		if a.Disabled {
			state = "off"
		}
		entries = append(entries, fmt.Sprintf("%s(%s,%s)", a.Username, strings.Join(services, ">"), state))
	}
	return fmt.Sprintf(
		"accounts=%q prefix=%q interval=%ds timeout=%ds autoLogin=%v checkInternet=%v verboseLog=%v logToFile=%v rememberPassword=%v language=%q portal=%s eportal=%s",
		strings.Join(entries, " "), c.AccountPrefix,
		c.CheckIntervalSeconds, c.HTTPTimeoutSeconds, c.AutoLogin, c.CheckInternet,
		c.VerboseLog, c.LogToFile, c.RememberPassword, c.Language, c.PortalBaseURL, c.EportalURL)
}

// Validate returns a field path to message key map, where the keys are looked up
// in the shared string table by the settings page. The paths address the login
// list by position, so the page can put every message next to the input it is
// about.
func (c *Config) Validate() map[string]string {
	errs := map[string]string{}
	if len(c.Accounts) == 0 {
		errs["accounts"] = "err.accountsRequired"
	}
	for i, a := range c.Accounts {
		// An entry that is disabled is not used, so it is not asked to be
		// complete: taking a half written one out of the running has to be
		// possible without deleting it.
		if a.Disabled {
			continue
		}
		at := "accounts." + strconv.Itoa(i)
		if strings.TrimSpace(a.Username) == "" {
			errs[at+".username"] = "err.accountRequired"
		}
		if a.Password == "" {
			errs[at+".password"] = "err.passwordRequired"
		}
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

// CompactSuffixes drops blank repeat entries and trims the rest before saving,
// keeping the order the service types are tried in.
func CompactSuffixes(list []string) []string {
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, suffix := range list {
		s := strings.TrimSpace(suffix)
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
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
