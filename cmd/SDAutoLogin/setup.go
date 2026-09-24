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
// wizard, which stays English.
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
// command, which is the only way in on a machine that has neither a desktop nor
// a browser; a background run is given its account beforehand or through the
// settings page, since nobody is there to answer questions.
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
	entry.Password = prompt(in, "password", "")

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
	cfg.RememberPassword = true

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
