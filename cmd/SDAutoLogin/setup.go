package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"sdautologin/internal/config"
	"sdautologin/internal/i18n"
	"sdautologin/internal/logging"
)

// languageChoice is one entry of the language picker used by the command line
// wizard. The wizard itself stays English.
type languageChoice struct {
	name string
	code string
}

var languages = []languageChoice{
	{"follow system", string(i18n.LangAuto)},
	{"Chinese (simplified)", string(i18n.LangZH)},
	{"English", string(i18n.LangEN)},
}

// currentLanguageChoice returns the menu number of the configured language.
func currentLanguageChoice() int {
	switch i18n.LangSetting() {
	case i18n.LangZH:
		return 2
	case i18n.LangEN:
		return 3
	default:
		return 1
	}
}

// runSetupWizard asks for the account details on stdin. It backs the "config"
// command, the only way in on a machine with neither a desktop nor a browser; a
// background run is given its account beforehand, since nobody is there to answer.
func runSetupWizard(cfg *config.Config) error {
	in := bufio.NewReader(os.Stdin)
	fmt.Println("=== campus network auto login: first run setup ===")
	fmt.Println("(press Enter to keep the value in brackets)")

	fmt.Println("interface language:")
	for i, l := range languages {
		fmt.Printf("  %d) %s\n", i+1, l.name)
	}
	if choice := prompt(in, "interface language", strconv.Itoa(currentLanguageChoice())); choice != "" {
		if n, err := strconv.Atoi(choice); err == nil && n >= 1 && n <= len(languages) {
			cfg.Language = languages[n-1].code
		}
	}
	i18n.SetLang(i18n.Lang(cfg.Language))

	// The wizard writes one account, enough for a machine with no display and no
	// browser to get going. More of them, and the order they are tried in, are
	// set from the settings page or with a repeated --account.
	entry := config.Account{}
	if len(cfg.Accounts) > 0 {
		entry = cfg.Accounts[0]
	}
	entry.Username = prompt(in, "account (student id / name / mail)", entry.Username)
	entry.Password = promptPassword(in, entry.Password)
	// Saved here and nowhere else: an entry without both is dropped by Attempts(),
	// so writing one down would end in a run that reports no account to sign in
	// with, pointing at the wizard that has just answered that all is well.
	if gap := accountGap(entry); gap != "" {
		err := fmt.Errorf("not saved: the account needs %s to sign in", gap)
		logging.Log.Error("%v", err)
		fmt.Fprintln(os.Stderr, err)
		return err
	}

	fmt.Println("service type (further ones, and their order, are set in the settings page):")
	for i, op := range config.Operators {
		fmt.Printf("  %d) %s\n", i+1, op.Name)
	}
	if choice := prompt(in, "service type", "1"); choice != "" {
		if n, err := strconv.Atoi(choice); err == nil && n >= 1 && n <= len(config.Operators) {
			entry.ISPs = []string{config.Operators[n-1].Suffix}
		}
	}
	cfg.Accounts = []config.Account{entry}
	if v := prompt(in, "check interval in seconds", strconv.Itoa(cfg.CheckIntervalSeconds)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 5 {
			cfg.CheckIntervalSeconds = n
		}
	}
	// The password the wizard read is written down only when the configuration is
	// told to keep it, and this used to be set whatever the file said: a run that
	// came here to change the check interval turned a deliberate "not kept in the
	// file" into a plain text password in config.json. Enter keeps what is in
	// force, which on a first run is the yes this wizard needs.
	cfg.RememberPassword = promptBool(in, "write the password into config.json (it is kept there in clear text)", cfg.RememberPassword)
	if !cfg.RememberPassword && entry.Password != "" {
		fmt.Fprintln(os.Stderr, "note: the password is not written down, so every run needs it again: pass --password, --password-file or $CREDENTIALS_DIRECTORY, or answer yes here")
	}

	if err := cfg.Save(); err != nil {
		logging.Log.Error("saving config: %v", err)
		fmt.Fprintf(os.Stderr, "not saved: %v\n", err)
		return err
	}
	fmt.Println("saved to", cfg.Path())
	return nil
}

func prompt(in *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := in.ReadString('\n')
	if line = strings.TrimSpace(line); line == "" {
		return def
	}
	return line
}

// promptBool asks a yes or no question, and Enter keeps the answer that is in
// force: a wizard run to change something else must not turn a setting around.
// Anything that is neither a yes nor a no keeps it as well, since the reader has
// then said nothing this question understands.
func promptBool(in *bufio.Reader, label string, current bool) bool {
	hint := "y/N"
	if current {
		hint = "Y/n"
	}
	fmt.Printf("%s [%s]: ", label, hint)
	line, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return current
}

// accountGap names what an entry is missing to sign in, and "" when it has both.
func accountGap(entry config.Account) string {
	username := strings.TrimSpace(entry.Username)
	switch {
	case username == "" && entry.Password == "":
		return "a username and a password"
	case username == "":
		return "a username"
	case entry.Password == "":
		return "a password"
	}
	return ""
}
