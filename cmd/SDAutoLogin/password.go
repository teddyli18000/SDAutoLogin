package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// promptPassword reads one line for a secret, with the echo off where the platform
// can do that, so it does not stay in the scrollback of the SSH session the wizard
// exists for. Where the echo cannot be turned off the answer is typed visibly and
// the reader is told so, and it comes from the same reader as the rest, so a piped
// setup still works.
func promptPassword(in *bufio.Reader, stored string) string {
	if stored != "" {
		// The stored secret is not printed in brackets the way the other defaults
		// are: keeping it off the screen is what this prompt is for.
		fmt.Print("password [Enter keeps the stored one]: ")
	} else {
		fmt.Print("password: ")
	}

	restore, hidden := echoOff(os.Stdin)
	if hidden {
		// The Enter key is not echoed either, so the line the cursor was left on
		// is ended here after the echo is back.
		defer fmt.Println()
		defer restore()
	} else {
		// Nothing here could stop the terminal from showing the answer: it is not
		// a terminal, or it refused the change, and the secret is typed where
		// whoever is looking at the screen can read it. Said out loud, since a
		// prompt that hides nothing must not look like one that hides something.
		fmt.Fprintln(os.Stderr, "warning: this terminal cannot hide what is typed, so the password will be visible where it is typed; use --password-file or the settings page to keep it off the screen")
	}
	line, _ := in.ReadString('\n')
	if secret := strings.TrimSpace(line); secret != "" {
		return secret
	}
	return stored
}
