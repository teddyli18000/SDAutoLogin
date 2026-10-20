package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The two shapes a password file comes in are told apart by the colon alone, so
// these cases are the whole rule.
func TestAPasswordFileIsOneSecretOrAList(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantSingle string
		wantList   []passwordEntry
	}{
		{
			name:       "one secret, and only the line ending comes off it",
			content:    "two spaces  \r\nsecond line\n",
			wantSingle: "two spaces  ",
		},
		{
			name:       "a secret with no trailing newline",
			content:    "secret",
			wantSingle: "secret",
		},
		{
			name:    "a list, split on the first colon",
			content: "s1234567:se:cret\ns7654321:other\n",
			wantList: []passwordEntry{
				{name: "s1234567", secret: "se:cret"},
				{name: "s7654321", secret: "other"},
			},
		},
		{
			name:    "blank lines in a list carry nothing",
			content: "s1234567:one\n\ns7654321:two\n",
			wantList: []passwordEntry{
				{name: "s1234567", secret: "one"},
				{name: "s7654321", secret: "two"},
			},
		},
		{
			name:       "a secret may end in a space",
			content:    "trailing  \n",
			wantSingle: "trailing  ",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			list, single, err := parsePasswordFile([]byte(c.content))
			if err != nil {
				t.Fatalf("parsePasswordFile: %v", err)
			}
			if single != c.wantSingle {
				t.Errorf("single = %q, want %q", single, c.wantSingle)
			}
			if len(list) != len(c.wantList) {
				t.Fatalf("list = %+v, want %+v", list, c.wantList)
			}
			for i := range list {
				if list[i] != c.wantList[i] {
					t.Errorf("list[%d] = %+v, want %+v", i, list[i], c.wantList[i])
				}
			}
		})
	}
}

// A file that says nothing, or says something contradictory, is refused with
// enough of a description to find the line. Guessing at one would sign in with
// whatever the guess produced.
func TestAPasswordFileThatCannotBeReadIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{name: "empty", content: "", want: "holds no password"},
		{name: "one newline", content: "\n", want: "holds no password"},
		{name: "a list of blank lines", content: "\n\n", want: "holds no password"},
		{name: "a line without a colon among named ones", content: "s1234567:one\nstray\n", want: "line 2 holds no colon"},
		{name: "a line with no account", content: ":one\n", want: "line 1 names no account"},
		{name: "a line with no password", content: "s1234567:\n", want: "line 1 gives s1234567 no password"},
		{name: "the same account twice", content: "s1234567:one\ns1234567:two\n", want: "line 2 names s1234567 a second time"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := parsePasswordFile([]byte(c.content))
			if err == nil {
				t.Fatal("a file that says nothing usable was accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}
}

// secretFile writes a password file and returns its path.
func secretFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "passwords")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// loadWithPasswords builds a configuration the way LoadConfig would, with the
// passwords read from the named file.
func loadWithPasswords(t *testing.T, accounts []Account, file string) (*Config, string) {
	t.Helper()
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = accounts
	cfg.PasswordFile = file
	notice := cfg.LoadPasswords()
	return cfg, notice
}

// The point of the whole feature: a password read from a file never lands in
// config.json, however the file was saved.
func TestAPasswordFromAFileIsNotWrittenToTheConfig(t *testing.T) {
	file := secretFile(t, "s1234567:fromthefile\n")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", ISPs: []string{""}}}
	cfg.PasswordFile = file
	if notice := cfg.LoadPasswords(); notice != "" {
		t.Fatalf("LoadPasswords: %s", notice)
	}
	if got := cfg.Accounts[0].Password; got != "fromthefile" {
		t.Fatalf("Password = %q, want the one from the file", got)
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "fromthefile") {
		t.Errorf("the password was written into the configuration file:\n%s", data)
	}
	// What the file records is read back rather than searched for as text: on
	// Windows the path is a name like C:\Users\... and JSON writes each
	// backslash twice, so a search for the path as it stands on disk would
	// report it missing while it is there.
	var recorded struct {
		PasswordFile string `json:"passwordFile"`
	}
	if err := json.Unmarshal(data, &recorded); err != nil {
		t.Fatalf("the file must stay valid JSON: %v", err)
	}
	if recorded.PasswordFile != file {
		t.Errorf("the password file recorded is %q, want %q:\n%s", recorded.PasswordFile, file, data)
	}
	// The source is for the page and is not part of the file either.
	if strings.Contains(string(data), "passwordSource") {
		t.Errorf("the source was written into the configuration file:\n%s", data)
	}
}

// One secret for the whole file belongs to the first entry, which is the shape a
// service given a single credential writes.
func TestOneSecretInTheFileBelongsToTheFirstEntry(t *testing.T) {
	file := secretFile(t, "theonlyone\n")
	cfg, notice := loadWithPasswords(t, []Account{
		{Username: "s1234567", ISPs: []string{""}},
		{Username: "s7654321", Password: "kept", ISPs: []string{""}},
	}, file)
	if notice != "" {
		t.Fatalf("notice = %q, want none", notice)
	}
	if cfg.Accounts[0].Password != "theonlyone" || cfg.Accounts[0].PasswordSource != SourceFile {
		t.Errorf("the first entry = %+v, want the secret from the file", cfg.Accounts[0])
	}
	if cfg.Accounts[1].Password != "kept" || cfg.Accounts[1].PasswordSource != "" {
		t.Errorf("the second entry was touched: %+v", cfg.Accounts[1])
	}
}

// A list fills the entries it names and leaves the others alone.
func TestAListFillsTheEntriesItNames(t *testing.T) {
	file := secretFile(t, "s7654321:second\n")
	cfg, notice := loadWithPasswords(t, []Account{
		{Username: "s1234567", Password: "stored", ISPs: []string{""}},
		{Username: "s7654321", ISPs: []string{""}},
	}, file)
	if notice != "" {
		t.Fatalf("notice = %q, want none", notice)
	}
	if cfg.Accounts[0].Password != "stored" || cfg.Accounts[0].PasswordSource != "" {
		t.Errorf("an entry the file does not name was changed: %+v", cfg.Accounts[0])
	}
	if cfg.Accounts[1].Password != "second" || cfg.Accounts[1].PasswordSource != SourceFile {
		t.Errorf("the named entry = %+v, want the secret from the file", cfg.Accounts[1])
	}
}

// A name no entry uses is reported, not guessed at: that report is what
// keeps a misread secret from becoming a sign-in.
func TestANameNoEntryUsesIsReported(t *testing.T) {
	file := secretFile(t, "s1234567:one\ns9999999:two\n")
	cfg, notice := loadWithPasswords(t, []Account{{Username: "s1234567", ISPs: []string{""}}}, file)
	if !strings.Contains(notice, "s9999999") {
		t.Errorf("notice = %q, want it to name the account no entry uses", notice)
	}
	// The entry that was named still got its password.
	if cfg.Accounts[0].Password != "one" {
		t.Errorf("the named entry was not filled: %+v", cfg.Accounts[0])
	}
}

// An entry the file leaves without a password is not attempted, which is the
// same treatment an entry with no password has always had.
func TestAnEntryTheFileLeavesEmptyIsNotAttempted(t *testing.T) {
	file := secretFile(t, "s7654321:second\n")
	cfg, _ := loadWithPasswords(t, []Account{
		{Username: "s1234567", ISPs: []string{""}},
		{Username: "s7654321", ISPs: []string{""}},
	}, file)
	attempts := cfg.Attempts()
	if len(attempts) != 1 || attempts[0].Username != "s7654321" {
		t.Errorf("attempts = %+v, want only the entry the file names", attempts)
	}
}

// A file that cannot be read is reported, and the run keeps whatever else it
// has.
func TestAPasswordFileThatIsNotThereIsReported(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	cfg, notice := loadWithPasswords(t, []Account{{Username: "s1234567", ISPs: []string{""}}}, missing)
	if !strings.Contains(notice, missing) {
		t.Errorf("notice = %q, want it to name the file", notice)
	}
	if len(cfg.Attempts()) != 0 {
		t.Error("an entry with no password was attempted")
	}
}

// A request that stops naming the password file must not leave the entry with a
// password that came from it: the secret is not in config.json, so carrying it
// over would write the very thing the file was chosen to keep out.
func TestClearingThePasswordFileDoesNotCarryTheSecretOver(t *testing.T) {
	file := secretFile(t, "s1234567:fromthefile\n")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", ISPs: []string{""}}}
	cfg.PasswordFile = file
	if notice := cfg.LoadPasswords(); notice != "" {
		t.Fatalf("LoadPasswords: %s", notice)
	}

	// The page sends the configuration back without the path, the way it would
	// after the reader cleared the field.
	next := cfg.PageBase()
	next.PasswordFile = ""
	if notice := next.ReconcilePasswords(cfg); notice != "" {
		t.Fatalf("ReconcilePasswords: %s", notice)
	}
	if got := next.Accounts[0].Password; got != "" {
		t.Errorf("Password = %q, want it empty: the file is no longer named", got)
	}
	if got := next.Accounts[0].PasswordSource; got != "" {
		t.Errorf("PasswordSource = %q, want it empty so the page reopens the field", got)
	}

	if err := next.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "fromthefile") {
		t.Errorf("the secret reached the configuration file:\n%s", data)
	}
	if strings.Contains(string(data), "passwordFile") {
		t.Errorf("the cleared path was written back:\n%s", data)
	}
}

// A request that keeps the path fills the entries again, so the page keeps
// showing where the password comes from.
func TestKeepingThePasswordFileNameRefillsTheEntries(t *testing.T) {
	file := secretFile(t, "s1234567:fromthefile\n")

	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", ISPs: []string{""}}}
	cfg.PasswordFile = file
	if notice := cfg.LoadPasswords(); notice != "" {
		t.Fatalf("LoadPasswords: %s", notice)
	}

	next := cfg.PageBase()
	if notice := next.ReconcilePasswords(cfg); notice != "" {
		t.Fatalf("ReconcilePasswords: %s", notice)
	}
	if next.Accounts[0].Password != "fromthefile" || next.Accounts[0].PasswordSource != SourceFile {
		t.Errorf("entry = %+v, want it still read from the file", next.Accounts[0])
	}
}

// A home directory that does not exist yet is the normal case on a router, where
// the package installs the program and nothing else creates the directory its
// settings go in. Saving makes it, whatever the log switch is set to.
func TestSavingMakesTheDirectoryItsSettingsGoIn(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not", "there", "yet")
	cfg, err := LoadConfig(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	info, err := os.Stat(home)
	if err != nil {
		t.Fatalf("the directory was not made: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", home)
	}
	// The directory holds the passwords whenever they are remembered, so nobody
	// else may read it, the same as the file itself. NTFS does not hand a mode
	// back: os.Stat reports 0777 for every directory there, and what keeps this
	// one to its owner on Windows is the access list the save writes before
	// anything goes in (restrictToOwner), not the mode MkdirAll was given, so
	// the mode is only what this platform has to check.
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("directory mode = %04o, want 0700", got)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "config.json")); err != nil {
		t.Errorf("the configuration was not written: %v", err)
	}

	// A second save into the same place keeps working, and does not tighten a
	// mode somebody chose for a directory that was already there.
	cfg.Accounts = []Account{{Username: "s1234567", Password: "p", ISPs: []string{""}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(home)
		if err != nil {
			t.Fatalf("the directory went missing: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("directory mode after a second save = %04o", got)
		}
	}
}

// TestAPasswordFileWithAByteOrderMark: an editor that writes "UTF-8 with BOM" must not
// make the mark part of a secret or of an account name, which is what the same mark in
// config.json is already dropped for.
func TestAPasswordFileWithAByteOrderMark(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantSingle string
		wantList   []passwordEntry
	}{
		{"one secret", "\ufeffsecret\n", "secret", nil},
		{"a list", "\ufeffs1234567:pass\n", "", []passwordEntry{{name: "s1234567", secret: "pass"}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			list, single, err := parsePasswordFile([]byte(c.content))
			if err != nil {
				t.Fatalf("parsePasswordFile: %v", err)
			}
			if single != c.wantSingle {
				t.Errorf("single = %q, want %q", single, c.wantSingle)
			}
			if len(list) != len(c.wantList) {
				t.Fatalf("list = %+v, want %+v", list, c.wantList)
			}
			for i := range list {
				if list[i] != c.wantList[i] {
					t.Errorf("list[%d] = %+v, want %+v", i, list[i], c.wantList[i])
				}
			}
		})
	}
}

// TestOnlyACarriageReturnSeparatesRecords: a file from an editor that ends its lines
// with a CR alone holds two records, not one record that carries the other as its
// password.
func TestOnlyACarriageReturnSeparatesRecords(t *testing.T) {
	list, single, err := parsePasswordFile([]byte("s1234567:one\rs7654321:two"))
	if err != nil {
		t.Fatalf("parsePasswordFile: %v", err)
	}
	if single != "" {
		t.Errorf("single = %q, want nothing", single)
	}
	want := []passwordEntry{{name: "s1234567", secret: "one"}, {name: "s7654321", secret: "two"}}
	if len(list) != len(want) {
		t.Fatalf("list = %+v, want %+v", list, want)
	}
	for i := range list {
		if list[i] != want[i] {
			t.Errorf("list[%d] = %+v, want %+v", i, list[i], want[i])
		}
	}
}

// TestAPasswordFileOfTwoLinesWithoutAColonIsRefused: one line with no colon is one
// password, and a second line is not something to guess at. Nothing is loaded, and
// neither line is repeated where a log would keep it.
func TestAPasswordFileOfTwoLinesWithoutAColonIsRefused(t *testing.T) {
	file := secretFile(t, "pass1\npass2\n")
	cfg, notice := loadWithPasswords(t, []Account{{Username: "s1234567", ISPs: []string{""}}}, file)
	if notice == "" {
		t.Fatal("a file of two lines with no account was accepted")
	}
	if !strings.Contains(notice, "2 lines") {
		t.Errorf("the report does not say how many lines the file holds: %q", notice)
	}
	if strings.Contains(notice, "pass1") || strings.Contains(notice, "pass2") {
		t.Errorf("the report repeated a line of the file: %q", notice)
	}
	if len(cfg.Attempts()) != 0 {
		t.Error("a password was taken from a file that says two things at once")
	}
}

// TestAOneLinePasswordFileThatNamesNoAccountIsRefused covers the other ambiguity: one
// line before a colon is either an account this list does not use or one password with
// a colon in it. Nothing is loaded, and what stands before the colon is not written
// down, since it is part of a secret often enough.
func TestAOneLinePasswordFileThatNamesNoAccountIsRefused(t *testing.T) {
	file := secretFile(t, "hunter2:swordfish\n")
	cfg, notice := loadWithPasswords(t, []Account{{Username: "s1234567", ISPs: []string{""}}}, file)
	if notice == "" {
		t.Fatal("a line naming an account the list does not use was accepted")
	}
	if strings.Contains(notice, "hunter2") || strings.Contains(notice, "swordfish") {
		t.Errorf("the report repeats part of the line, which may be the password: %q", notice)
	}
	if !strings.Contains(notice, "one line") {
		t.Errorf("the report does not describe the file: %q", notice)
	}
	if len(cfg.Attempts()) != 0 {
		t.Error("a password was guessed from a line that says two things at once")
	}
	if cfg.Accounts[0].Password != "" {
		t.Errorf("an entry was given a password from an ambiguous line: %+v", cfg.Accounts[0])
	}
}

// TestAPasswordFileThatIsNotARegularFileIsRefused: opening a fifo waits for a writer
// and reading a device never ends, so neither is a password file.
func TestAPasswordFileThatIsNotARegularFileIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("there are no fifos on Windows")
	}
	fifo := filepath.Join(t.TempDir(), "passwords")
	if err := exec.Command("mkfifo", fifo).Run(); err != nil {
		t.Skipf("cannot make a fifo here: %v", err)
	}
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{Username: "s1234567", ISPs: []string{""}}}
	cfg.PasswordFile = fifo

	done := make(chan string, 1)
	go func() { done <- cfg.LoadPasswords() }()
	select {
	case notice := <-done:
		if !strings.Contains(notice, "not a regular file") {
			t.Errorf("notice = %q, want it to say what the file is", notice)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading the password file blocked on a fifo")
	}
}

// TestAPasswordFilePastTheBoundIsRefused: the read is bounded, so a path that points at
// something that is not a list of passwords cannot fill memory.
func TestAPasswordFilePastTheBoundIsRefused(t *testing.T) {
	file := filepath.Join(t.TempDir(), "passwords")
	big := make([]byte, maxPasswordFileBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(file, big, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, notice := loadWithPasswords(t, []Account{{Username: "s1234567", ISPs: []string{""}}}, file)
	if !strings.Contains(notice, "larger than") {
		t.Errorf("notice = %q, want it to say the file is too large to be one", notice)
	}
	if len(cfg.Attempts()) != 0 {
		t.Error("a password was taken from a file of that size")
	}
}

// TestALoosePasswordFileIsTightenedAndReported: the passwords are in clear text, so a
// file the deployment left readable by everybody is put right and said out loud, the
// way config.json is.
func TestALoosePasswordFileIsTightenedAndReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the mode bits are a Unix thing; Windows is given an access list instead")
	}
	file := secretFile(t, "topsecret\n")
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, notice := loadWithPasswords(t, []Account{{Username: "s1234567", ISPs: []string{""}}}, file)
	if !strings.Contains(notice, "0644") {
		t.Errorf("the report does not name the mode it found: %q", notice)
	}
	if got := fileMode(t, file); got != 0o600 {
		t.Errorf("the password file is %04o after it was read, want 0600", got)
	}
	if cfg.Accounts[0].Password != "topsecret" {
		t.Errorf("the secret was not read once the mode was put right: %+v", cfg.Accounts[0])
	}
}

// TestTheNoticeAboutThePasswordsIsTheOneTheCallerLogs covers the line the program keeps
// to hand: the passwords are read at startup and again when the page sends a
// configuration back, and the caller has one place to ask what happened instead of each
// of them remembering. A notice that was dropped would leave a run whose passwords were
// never read looking like one that had nothing to read.
func TestTheNoticeAboutThePasswordsIsTheOneTheCallerLogs(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "passwords")
	cfg, notice := loadWithPasswords(t, []Account{{Username: "s1234567", ISPs: []string{""}}}, missing)
	if notice == "" {
		t.Fatal("a password file that is not there was read without a word")
	}
	if got := cfg.PasswordNotice(); got != notice {
		t.Errorf("PasswordNotice() = %q, want the notice the load produced, %q", got, notice)
	}

	found := secretFile(t, "s1234567:fromthefile\n")
	cfg, notice = loadWithPasswords(t, []Account{{Username: "s1234567", ISPs: []string{""}}}, found)
	if notice != "" {
		t.Fatalf("a password file that was read as it stands was reported: %q", notice)
	}
	if got := cfg.PasswordNotice(); got != "" {
		t.Errorf("PasswordNotice() = %q, want nothing to say after a password file that was read", got)
	}
}
