// Package config reads and writes config.json and holds the settings type
// every other package works with.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"sdautologin/internal/i18n"
	"sdautologin/internal/update"
)

// Account is one entry of the login list: an account and the service types it
// may be used with, in the order they are tried. The portal allows one session
// per client address, so the list is a failover order: an entry that is out of
// credit, whose password changed or that was closed gives way to the next one.
type Account struct {
	Username string `json:"username"`
	// Password is empty in the file when the password is not remembered, and
	// empty in what the settings page is given: the page writes this field and
	// never reads it, so a stored secret is never sent to a browser.
	Password string `json:"password,omitempty"`
	// PasswordSource is not part of the file. It records where the password in
	// memory came from, so one read from a password file is not written back into
	// config.json and the page can say where a password is without being handed
	// one.
	PasswordSource string `json:"passwordSource,omitempty"`
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

	// RememberPassword decides whether the passwords are written to disk. One
	// switch covers the whole list, since the mode the program is for needs them
	// all on.
	RememberPassword bool `json:"rememberPassword"`

	// PasswordFile names a file the passwords are read from. No colon on any line
	// makes it one password, for the first entry; a colon makes it
	// "account:password" lines. What is read from it is never written back here.
	PasswordFile string `json:"passwordFile,omitempty"`

	// passwordNotice is what loading the password file had to report, for the
	// caller to log. It is not part of the file.
	passwordNotice string

	// PortalBaseURL is the portal home page, which embeds the client IP the
	// portal sees (the NAT egress address, not the local one).
	PortalBaseURL string `json:"portalBaseUrl"`
	// EportalURL is the Dr.COM eportal root, on port 801.
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
	// collects the console output, and the file is the same text on the flash.
	LogToFile bool `json:"logToFile"`
	// SwitchToPreferredService signs in again when the session is up on a
	// service type other than the first one of its entry. A session already up
	// (signed in by hand, or by an earlier run) stays where it is; with this
	// off, its service type is only reported to the page.
	SwitchToPreferredService bool     `json:"switchToPreferredService"`
	ProbeURLs                []string `json:"probeUrls"`
	JsVersion                string   `json:"jsVersion"`
	// Language selects the interface language: auto, zh or en.
	Language string `json:"language"`

	// CheckForUpdates looks for a newer release and reports it. Off by default:
	// it is a request to a server the reader did not choose this run, and whether
	// the program makes it is theirs to decide. Nothing is installed unless this
	// copy can replace itself, which is reported either way.
	CheckForUpdates bool `json:"checkForUpdates"`
	// UpdateChannel is which releases an update may move to: "stable" for the
	// ones everyone gets, "prerelease" to try a version before it is one. An
	// empty or unknown value is read as stable.
	UpdateChannel string `json:"updateChannel"`

	// runSecrets are the passwords this configuration was told were given to this
	// run alone, by --password or by --account. PageBase fills the list in from the
	// configuration a page request was built on, and marshal leaves a password that
	// is one of them out of the file: a page storing an unrelated setting is not the
	// operator asking for a secret meant for one run to be written down. --save is
	// that request, and it saves the configuration this program read, which has no
	// list. It is not part of the file.
	runSecrets []string

	path string
}

// The bounds normalize holds every value to, whatever the page or a hand edited
// file asked for. The upper ones matter: both values are multiplied into a
// time.Duration, and one large enough to wrap it becomes negative, which fires
// the timer at once and turns the check loop into a hot loop against the portal.
const (
	// A check a day apart is already far past useful.
	maxCheckIntervalSeconds = 24 * 60 * 60
	// A request that takes longer than this has failed whatever the portal says.
	maxHTTPTimeoutSeconds = 15 * 60

	// UpdateChannelStable and UpdateChannelPrerelease are the two update
	// channels. They are stored as written, so a configuration file stays
	// readable, and are checked where they are used.
	UpdateChannelStable     = "stable"
	UpdateChannelPrerelease = "prerelease"
)

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
		// On: the order of an entry's service types is there to be followed, and
		// the program signs in as the first one whenever it signs in itself. A
		// session that was already up moves only when this option is on.
		SwitchToPreferredService: true,
		Language:                 string(i18n.LangAuto),
		// Off, and the channel that cannot surprise anyone. The switch is the
		// reader's to turn on: see the field's comment.
		CheckForUpdates: false,
		UpdateChannel:   UpdateChannelStable,
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
	// A file written by hand, by an installer or by an older version has no page
	// to report to, so what is wrong with it is reported here. The values this
	// program cannot use are put back to the ones it ships with first: an address
	// that did not pass is not where the passwords are submitted, not even on the
	// path that keeps going with what it could read.
	loadErr := cfg.checkLoaded()
	cfg.markStoredPasswords()
	cfg.passwordNotice = cfg.loadPasswords()
	if loadErr != nil {
		return cfg, fmt.Errorf("%s: %w", path, loadErr)
	}
	return cfg, nil
}

func (c *Config) Path() string { return c.path }

// PasswordNotice is what reading the password file had to report, or "" when
// there was nothing to say. The caller logs it; nothing here writes to a log.
func (c *Config) PasswordNotice() string { return c.passwordNotice }

// UpdateChannelValue is which releases the program may move to. An empty or
// unknown name reads as stable: the file is written by hand as often as by the
// page, and a value this program does not know must not widen what it takes.
func (c *Config) UpdateChannelValue() update.Channel {
	if update.ValidChannel(c.UpdateChannel) {
		return update.Channel(c.UpdateChannel)
	}
	return update.Stable
}

// markStoredPasswords records that a password already in memory came from this
// file, so that the page can name its source. A password read from a password
// file has its source set where it is read.
func (c *Config) markStoredPasswords() {
	for i := range c.Accounts {
		if c.Accounts[i].Password != "" && c.Accounts[i].PasswordSource == "" {
			c.Accounts[i].PasswordSource = SourceConfig
		}
	}
}

// LoadPasswords fills the entries the file names and reports what it could not
// do. An entry it does not name keeps what it had, a name no entry uses is
// reported, and an entry left without a password is not attempted.
func (c *Config) LoadPasswords() string {
	c.passwordNotice = c.loadPasswords()
	return c.passwordNotice
}

// maxPasswordFileBytes bounds the file the passwords are read from. A password is a
// short line, so a file far larger than a list of them is a wrong path or a device
// that never ends, and neither is read into memory.
const maxPasswordFileBytes = 64 << 10

func (c *Config) loadPasswords() string {
	path := strings.TrimSpace(c.PasswordFile)
	if path == "" {
		return ""
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Sprintf("cannot read the password file %q: %v", path, err)
	}
	target := path
	if info.Mode()&os.ModeSymlink != 0 {
		// A link is how a secret is pointed at without being copied, so it is
		// followed; what it points at is then held to the same rules as the file
		// itself would be.
		if target, err = filepath.EvalSymlinks(path); err != nil {
			return fmt.Sprintf("cannot read the password file %q: %v", path, err)
		}
		if info, err = os.Stat(target); err != nil {
			return fmt.Sprintf("cannot read the password file %q: %v", path, err)
		}
	}
	// Only a regular file is read: a fifo or a device is not a password file, and
	// opening one can block for ever while reading one may never end.
	if !info.Mode().IsRegular() {
		return fmt.Sprintf("the password file %q is not a regular file (%s), so it is not read", path, info.Mode().Type())
	}
	if info.Size() > maxPasswordFileBytes {
		return fmt.Sprintf("the password file %q is %d bytes, which is larger than a list of passwords can be (%d bytes)",
			path, info.Size(), maxPasswordFileBytes)
	}
	// What the file lets other users do is checked before it is read: the passwords
	// in it are in clear text, and a file the deployment left readable by everybody
	// is one this program says so about rather than one it reads in silence.
	notice, read := passwordFileAccess(path, info)
	if !read {
		return notice
	}

	f, err := os.Open(target)
	if err != nil {
		return joinNotes(notice, fmt.Sprintf("cannot read the password file %q: %v", path, err))
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPasswordFileBytes+1))
	if err != nil {
		return joinNotes(notice, fmt.Sprintf("cannot read the password file %q: %v", path, err))
	}
	if len(data) > maxPasswordFileBytes {
		return joinNotes(notice, fmt.Sprintf("the password file %q grew past %d bytes while it was being read", path, maxPasswordFileBytes))
	}
	return joinNotes(notice, c.fillPasswords(path, data))
}

// fillPasswords fills the entries the file names and reports what it could not do.
// An entry it does not name keeps what it had, a name no entry uses is reported,
// and an entry left without a password is not attempted.
func (c *Config) fillPasswords(path string, data []byte) string {
	shape := describePasswordFile(data)
	switch {
	case shape.lines == 0:
		return fmt.Sprintf("the password file %q holds no password", path)
	case !shape.named && shape.lines > 1:
		// Without a colon the file is one password, and one only: a second line
		// would otherwise be dropped without a word, and a password split over two
		// lines is not something to guess at.
		return fmt.Sprintf("the password file %q holds %d lines and none of them names an account; one password has to be the only line, and an account list needs account:password on every line",
			path, shape.lines)
	}
	list, single, err := parsePasswordFile(data)
	if err != nil {
		return fmt.Sprintf("the password file %q %v", path, err)
	}
	if single != "" {
		// One secret for the whole file belongs to the first entry, which is the
		// shape a service given a single credential writes.
		if len(c.Accounts) == 0 {
			return fmt.Sprintf("the password file %q holds one password, and no entry of the login list can take it", path)
		}
		c.Accounts[0].Password = single
		c.Accounts[0].PasswordSource = SourceFile
		return ""
	}

	byName := make(map[string]int, len(c.Accounts))
	for i := range c.Accounts {
		byName[c.Accounts[i].Username] = i
	}
	unknown := make([]string, 0, len(list))
	for _, e := range list {
		i, ok := byName[e.name]
		if !ok {
			unknown = append(unknown, e.name)
			continue
		}
		c.Accounts[i].Password = e.secret
		c.Accounts[i].PasswordSource = SourceFile
	}
	if len(unknown) == 0 {
		return ""
	}
	if len(list) == 1 {
		// One line before a colon names an account this list does not use, or it
		// is one password that contains a colon: a password written as a single
		// line looks the same either way. Nothing is loaded, and the line is not
		// repeated here, because what stands before the colon is part of a secret
		// often enough that it does not belong in a log or on the page.
		return fmt.Sprintf("the password file %q holds one line, and no entry of the login list is named before its colon; it is either a name this list does not use or one password containing a colon, and nothing was loaded from it", path)
	}
	names := make([]string, 0, len(unknown))
	for _, name := range unknown {
		names = append(names, strconv.Quote(name))
	}
	return fmt.Sprintf("the password file %q names %s, and no entry of the login list uses that account",
		path, strings.Join(names, ", "))
}

// Clone returns a configuration that shares nothing mutable with c. A settings
// request is decoded onto the clone, so an invalid request leaves the running
// configuration untouched; a plain struct copy would still share the arrays.
func (c *Config) Clone() *Config {
	out := *c
	out.Accounts = slices.Clone(c.Accounts)
	for i := range out.Accounts {
		out.Accounts[i].ISPs = slices.Clone(c.Accounts[i].ISPs)
	}
	out.ProbeURLs = slices.Clone(c.ProbeURLs)
	out.runSecrets = slices.Clone(c.runSecrets)
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
	// The upper bounds are the ones at their definitions above: a larger value
	// would wrap the time.Duration and fire the timer at once.
	if c.CheckIntervalSeconds > maxCheckIntervalSeconds {
		c.CheckIntervalSeconds = maxCheckIntervalSeconds
	}
	if c.HTTPTimeoutSeconds > maxHTTPTimeoutSeconds {
		c.HTTPTimeoutSeconds = maxHTTPTimeoutSeconds
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
	// The prefix goes in front of every account before it is submitted, so an
	// empty one submits a bare account the portal reads as a different kind of
	// login. It is filled in like the other values a file may leave out.
	if strings.TrimSpace(c.AccountPrefix) == "" {
		c.AccountPrefix = defaultConfig().AccountPrefix
	}
	switch i18n.Lang(c.Language) {
	case i18n.LangZH, i18n.LangEN:
	default:
		c.Language = string(i18n.LangAuto)
	}
}

// migrate moves the single account an older file holds into the login list. The
// old fields are cleared either way, so a saved file carries the list alone.
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

// usable reports whether this entry has both an account and a password.
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
		// An account with no suffix is a session signed in without one, and that
		// is the campus network itself: the portal echoes what a login was
		// submitted with, so a suffix it does not name was not sent. An entry
		// that lists it answers; otherwise its first service type is a guess.
		if strings.EqualFold(a.Username, stripped) {
			// A suffix of "" is the campus network itself and the loop above already
			// answered for an entry that lists it. This is reached only for an entry
			// that names other service types, and it has to stand on its own: a
			// configuration can be built in memory without going through normalize,
			// which is what fills an empty list with the campus network, so an entry
			// with no service types at all arrives here and must not be indexed.
			if slices.Contains(a.ISPs, "") {
				return i, ""
			}
			if len(a.ISPs) == 0 {
				return i, ""
			}
			return i, a.ISPs[0]
		}
	}
	return -1, ""
}

// AttemptFor returns the attempt that submits one entry of the login list with
// one of its service types. It reports false when there is no such attempt: the
// position names no entry, the entry is switched off or cannot be submitted, or
// the service type is not one the entry may be used with.
func (c *Config) AttemptFor(index int, isp string) (Attempt, bool) {
	if index < 0 || index >= len(c.Accounts) {
		return Attempt{}, false
	}
	entry := c.Accounts[index]
	if entry.Disabled || !entry.usable() || !slices.Contains(entry.ISPs, isp) {
		return Attempt{}, false
	}
	return Attempt{
		Index:    index,
		Username: entry.Username,
		Password: entry.Password,
		ISP:      isp,
	}, true
}

// PreferredFor returns the attempt the session the portal reports should be on:
// the first service type of the entry that account belongs to. It reports false
// when the account names no entry, the entry is switched off or cannot be
// submitted, or the session is already on the first service type.
func (c *Config) PreferredFor(reported string) (Attempt, bool) {
	index, isp := c.Match(reported)
	if index < 0 || len(c.Accounts[index].ISPs) == 0 {
		return Attempt{}, false
	}
	first := c.Accounts[index].ISPs[0]
	if first == isp {
		return Attempt{}, false
	}
	return c.AttemptFor(index, first)
}

// SwitchTo returns the attempt that moves the reported session onto a service
// type of the entry that account belongs to, as the settings page asks. It
// reports false when the account names no entry, the entry is switched off or
// cannot be submitted, the type is not listed, or the session is on it.
func (c *Config) SwitchTo(reported, isp string) (Attempt, bool) {
	index, used := c.Match(reported)
	if index < 0 || used == isp {
		return Attempt{}, false
	}
	return c.AttemptFor(index, isp)
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

// HasAccounts reports whether the login list names an account, whatever state
// its password is in. That is a different question from Configured: a
// configuration whose passwords are deliberately not stored has still been set
// up.
func (c *Config) HasAccounts() bool {
	for i := range c.Accounts {
		if strings.TrimSpace(c.Accounts[i].Username) != "" {
			return true
		}
	}
	return false
}

// configFileMode is the mode config.json is written with. The file holds the
// passwords in clear text when they are remembered, so it belongs to its owner
// alone: the program runs as that user, and campus accounts are not shared on a
// machine several people use.
const configFileMode = 0o600

// configDirMode is the mode of the directory holding config.json, applied only
// when it is created. That directory holds the passwords whenever they are
// remembered, so it belongs to its owner alone.
const configDirMode = 0o700

// Save writes config.json, omitting the passwords when they are not remembered.
// The file is replaced in one step: the new text is written into a file beside it
// and put in place with a rename, so a write that fails part way through leaves the
// configuration that was there alone instead of a truncated one.
func (c *Config) Save() error {
	c.normalize()
	data, err := c.marshal()
	if err != nil {
		return err
	}
	// The directory is made here, not left to the caller: a home directory that
	// does not exist yet is the normal case on a router, where the package installs
	// the program and nothing else creates it.
	dir := filepath.Dir(c.path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, configDirMode); err != nil {
		return err
	}
	if err := checkWritePath(dir, c.path); err != nil {
		return err
	}

	// The text goes into a file of its own first, with the mode it is meant to
	// keep, and the access list Windows needs is set on that empty file: nothing
	// secret is ever in a file whose list did not come out right.
	f, err := os.CreateTemp(dir, filepath.Base(c.path)+".tmp")
	if err != nil {
		return err
	}
	staged := f.Name()
	placed := false
	defer func() {
		if !placed {
			f.Close()
			os.Remove(staged)
		}
	}()
	// A file that is to hold a password is written only if it can be kept from
	// every account but its owner's, and a filesystem with no access lists (a
	// FAT stick, a network share) refuses the list entirely: rather than write a
	// password anyone may read, nothing is written and the caller is told. A file
	// with no password in it is written either way, since there is nothing there
	// to keep, and refusing it would leave a machine with such a filesystem
	// unable to save any setting at all.
	holdsSecret := bytes.Contains(data, []byte(`"password":`))
	if err := RestrictToOwner(staged); err != nil {
		if holdsSecret {
			return fmt.Errorf("the access list of the file that would hold the passwords could not be set, so nothing was written: %w", err)
		}
	}
	// CreateTemp makes the file 0600 already; this acts on the handle, so the mode
	// of the file that is put in place is this one whatever an umask would allow.
	if err := f.Chmod(configFileMode); err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(staged, c.path); err != nil {
		return err
	}
	placed = true
	syncDir(dir)
	return nil
}

// checkWritePath refuses to write where the file could be redirected somewhere else
// or replaced by another user. config.json holds the passwords in clear text, so the
// directory it goes in and the file itself have to be this user's own, with no
// symbolic link in the way: the write, the chmod and the access list all follow a
// link, so a link in either place is what the passwords are written into.
func checkWritePath(dir, path string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("the directory %q is a symbolic link, so a file written there would land wherever it points; name the directory itself", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}
	if err := checkDirOwner(dir); err != nil {
		return err
	}
	info, err = os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Nothing there yet: the file that is about to be made is this user's.
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%q is a symbolic link, and the passwords are not written through one: that would replace whatever it points at", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file, so it is not where the configuration is written", path)
	}
	return nil
}

// syncDir asks the directory to make the rename that just happened durable. A
// failure here is not one the caller can act on: it means the configuration may
// come back as it was after a crash, not that what was saved is wrong.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	d.Sync()
	d.Close()
}

// joinNotes puts two lines together, leaving out the one that says nothing.
func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// checkLoaded refuses a configuration that came off disk with something in it this
// program cannot use, and puts the values it cannot use back to the ones this build
// ships with. Nothing else checks a file written by hand, by an installer or by an
// older version: the settings page validates what the page sends, and a file has no
// page. The message names the field, since it is read in a log or on a console
// rather than next to the input it is about.
func (c *Config) checkLoaded() error {
	problems := make([]string, 0, 3)
	if !ValidHTTPURL(c.PortalBaseURL) {
		problems = append(problems, fmt.Sprintf("portalBaseUrl=%q", c.PortalBaseURL))
		c.PortalBaseURL = defaultConfig().PortalBaseURL
	}
	if !ValidHTTPURL(c.EportalURL) {
		problems = append(problems, fmt.Sprintf("eportalUrl=%q", c.EportalURL))
		c.EportalURL = defaultConfig().EportalURL
	}
	for i, probe := range c.ProbeURLs {
		if strings.TrimSpace(probe) != "" && !ValidHTTPURL(probe) {
			problems = append(problems, fmt.Sprintf("probeUrls[%d]=%q", i, probe))
		}
	}
	if !validAccountPrefix(c.AccountPrefix) {
		problems = append(problems, fmt.Sprintf("accountPrefix=%q", c.AccountPrefix))
		c.AccountPrefix = defaultConfig().AccountPrefix
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the configuration names values this program cannot use (%s); the portal addresses and the account prefix were put back to the defaults",
		strings.Join(problems, ", "))
}

// Protect checks that the file holding the passwords cannot be read by anyone
// but its owner and puts it right where it can, returning a line for the log or
// an empty string. Save covers the file it writes; this covers the one on disk.
func (c *Config) Protect() string { return protect(c.path) }

func (c *Config) marshal() ([]byte, error) {
	type persistConfig Config
	v := persistConfig(*c)
	// The list is always copied: the source and the passwords are what the page is
	// told and never what the file holds. A password read from a password file is
	// dropped here as well, or the file would end up holding the secret it was
	// meant to keep out of it.
	v.Accounts = make([]Account, len(c.Accounts))
	copy(v.Accounts, c.Accounts)
	for i := range v.Accounts {
		source := c.Accounts[i].PasswordSource
		v.Accounts[i].PasswordSource = ""
		// A password read from a file is not in config.json, and neither is one
		// this run was given and a page left in place: that secret was handed over
		// for this run alone, and a page saving an unrelated setting is not the
		// operator asking for it to be written down. The password itself is what
		// says so, so a request that sends one back cannot put it in the file.
		fromRun := v.Accounts[i].Password != "" && slices.Contains(c.runSecrets, v.Accounts[i].Password)
		if !c.RememberPassword || source == SourceFile || fromRun {
			v.Accounts[i].Password = ""
		}
	}
	return json.MarshalIndent(v, "", "  ")
}

// PageBase is the configuration a settings request is decoded onto: a copy of the
// running one with every password that came from a password file taken out, every
// source cleared, and every secret this run was given recorded. The page sends no
// source back, so a password left in place could not be told from one the reader
// typed; the recorded secret is what keeps a save from writing down the one kind of
// password that was handed over for this run alone. The secret itself stays in memory,
// since the account it belongs to still has to sign in this run.
func (c *Config) PageBase() *Config {
	out := c.Clone()
	out.runSecrets = nil
	for i := range out.Accounts {
		switch out.Accounts[i].PasswordSource {
		case SourceFile:
			// Read from a file: the page is never handed it, and a save that does not
			// name that file again must not turn the secret into a stored one.
			out.Accounts[i].Password = ""
		case SourceRun:
			if out.Accounts[i].Password != "" {
				out.runSecrets = append(out.runSecrets, out.Accounts[i].Password)
			}
		}
		// Every label goes: a request is decoded onto this copy, and where a password
		// came from is not the request's to say. A secret this run was given is marked
		// again by KeepPasswordsFrom, which is what puts that one label back.
		out.Accounts[i].PasswordSource = ""
	}
	return out
}

// ReconcilePasswords works out where each password comes from once a request
// has been decoded onto PageBase: what it left empty is taken from stored, and
// the file it now names is read again. It returns a line for the log, or "".
func (c *Config) ReconcilePasswords(stored *Config) string {
	c.KeepPasswordsFrom(stored)
	c.markStoredPasswords()
	return c.LoadPasswords()
}

// KeepPasswordsFrom fills the empty passwords in from the configuration this one was
// built from. A password that came from a password file is not inherited: it is not
// in config.json, and carrying it over would write it there. One this run was given
// is inherited along with its label, so the account keeps working until the program
// stops, the page is still told where the password is, and a save of this
// configuration leaves the secret out of the file the same way.
func (c *Config) KeepPasswordsFrom(stored *Config) {
	if stored == nil {
		return
	}
	inherited := make([]bool, len(stored.Accounts))
	for j := range stored.Accounts {
		inherited[j] = stored.Accounts[j].PasswordSource == SourceFile
	}
	for i := range c.Accounts {
		if c.Accounts[i].Password != "" {
			continue
		}
		// The entry in the same place, when it is the same account: the case of a
		// list that nothing but an unrelated setting was done to.
		if i < len(stored.Accounts) && !inherited[i] && sameAccount(stored.Accounts[i], c.Accounts[i]) {
			inherited[i] = true
			c.inheritPassword(i, stored.Accounts[i])
			continue
		}
		// Otherwise the entry the account came from, wherever it has moved to.
		for j := range stored.Accounts {
			if inherited[j] || !sameAccount(stored.Accounts[j], c.Accounts[i]) {
				continue
			}
			inherited[j] = true
			c.inheritPassword(i, stored.Accounts[j])
			break
		}
	}
}

// inheritPassword takes the password of one stored entry into entry i, and the label
// that says where it came from with it: a secret this run was given is marked again,
// so what is written from this configuration leaves it out.
func (c *Config) inheritPassword(i int, from Account) {
	c.Accounts[i].Password = from.Password
	if from.PasswordSource == SourceRun {
		c.Accounts[i].PasswordSource = SourceRun
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
	line := fmt.Sprintf(
		"accounts=%q prefix=%q interval=%ds timeout=%ds autoLogin=%v checkInternet=%v verboseLog=%v logToFile=%v rememberPassword=%v switchToPreferredService=%v language=%q portal=%q eportal=%q",
		strings.Join(entries, " "), c.AccountPrefix,
		c.CheckIntervalSeconds, c.HTTPTimeoutSeconds, c.AutoLogin, c.CheckInternet,
		c.VerboseLog, c.LogToFile, c.RememberPassword, c.SwitchToPreferredService,
		c.Language, c.PortalBaseURL, c.EportalURL)
	// Where the passwords go is part of what describes this configuration, so a line
	// that describes it says when that address is not the one this build was made
	// for.
	if notice := c.PortalNotice(); notice != "" {
		return line + " " + notice
	}
	return line
}

// PortalNotice says that the portal address is not the one this build ships with,
// which is where the passwords are submitted. It is a warning and not a refusal: a
// deployment on another campus is a real one, and the address is the operator's to
// set. Every line that describes the configuration carries it, so a change of
// address is recorded in the log the next start writes and in the line a save from
// the page writes, without one host being hard coded as the only one allowed.
func (c *Config) PortalNotice() string {
	same := defaultConfig()
	changed := make([]string, 0, 2)
	if host := urlHost(c.PortalBaseURL); host != urlHost(same.PortalBaseURL) {
		changed = append(changed, fmt.Sprintf("portalBaseUrl (host %q)", host))
	}
	if host := urlHost(c.EportalURL); host != urlHost(same.EportalURL) {
		changed = append(changed, fmt.Sprintf("eportalUrl (host %q)", host))
	}
	if len(changed) == 0 {
		return ""
	}
	return fmt.Sprintf("note: the portal address is not the one this build ships with: %s; the passwords are submitted there",
		strings.Join(changed, " and "))
}

// urlHost is the host of an address, or "" when it cannot be read as one.
func urlHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Host
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
			// With a password file named, the entry may be missing from it rather
			// than missing a password, and renaming an account is the way that
			// happens. Saying so points at the file instead of at a field the page
			// has closed.
			if c.PasswordFile != "" {
				errs[at+".password"] = "err.passwordNotInFile"
			} else {
				errs[at+".password"] = "err.passwordRequired"
			}
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
	// The prefix is submitted as part of every account, so one that is empty or
	// that carries a character which changes the account string is refused rather
	// than turned into a sign-in that fails with nothing to show for it. The string
	// table has no key of its own for the prefix, so the generic one is used.
	if !validAccountPrefix(c.AccountPrefix) {
		errs["accountPrefix"] = "err.badRequest"
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

// ValidHTTPURL reports whether raw is an absolute http or https address this program
// may submit an account to. A fragment is refused: a query is added to the address,
// and everything after a '#' is a fragment, so a '#' in the address silently drops
// every parameter that follows it. Whitespace and control characters are refused as
// well, since an address that carries one is not the address that was meant and
// cannot be read back as it stands.
func ValidHTTPURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	if strings.ContainsFunc(trimmed, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return false
	}
	if strings.ContainsRune(trimmed, '#') {
		return false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

// validAccountPrefix reports whether the prefix that is put in front of an account
// before it is submitted is one this program can send as it stands. The account goes
// into the request as one value, so a space, a control character or a character that
// changes the shape of the address is refused here rather than becoming a sign-in
// that fails for a reason nobody can see.
func validAccountPrefix(prefix string) bool {
	if prefix == "" || strings.TrimSpace(prefix) != prefix {
		return false
	}
	for _, r := range prefix {
		if r < 0x20 || r == 0x7f {
			return false
		}
		if strings.ContainsRune(" #?&=/%", r) {
			return false
		}
	}
	return true
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
