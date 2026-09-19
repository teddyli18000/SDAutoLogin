package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// runSetupWizard asks for the account details on stdin. It is used on
// platforms without a settings window (Linux, macOS, plain terminals).
func runSetupWizard(cfg *Config) {
	in := bufio.NewReader(os.Stdin)
	fmt.Println("=== campus network auto login: first run setup ===")
	fmt.Println("(press Enter to keep the value in brackets)")

	cfg.Username = prompt(in, "account (student id / name / mail)", cfg.Username)
	cfg.Password = prompt(in, "password", "")

	fmt.Println("service type:")
	for i, op := range Operators {
		fmt.Printf("  %d) %s\n", i+1, op.Name)
	}
	if choice := prompt(in, "service type", "1"); choice != "" {
		if n, err := strconv.Atoi(choice); err == nil && n >= 1 && n <= len(Operators) {
			cfg.AccountSuffix = Operators[n-1].Suffix
		}
	}
	if v := prompt(in, "check interval in seconds", strconv.Itoa(cfg.CheckIntervalSeconds)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 5 {
			cfg.CheckIntervalSeconds = n
		}
	}
	cfg.RememberPassword = true

	if err := cfg.Save(); err != nil {
		Log.Error("saving config: %v", err)
		return
	}
	fmt.Println("saved to", cfg.Path())
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
