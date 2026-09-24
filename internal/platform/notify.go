package platform

import "strings"

// notifyCommand is how a desktop is asked for a desktop notification, or
// ("", nil) when there is no helper for it. It is kept apart from Notify, which
// each system provides for itself, so the wording of the command can be tested
// without running one.
func notifyCommand(goos, title, body string) (string, []string) {
	switch goos {
	case "darwin":
		// osascript runs AppleScript source rather than taking the text as an
		// argument, so both strings go in as quoted literals.
		return "osascript", []string{"-e",
			"display notification " + appleScriptString(body) +
				" with title " + appleScriptString(title)}
	default:
		// notify-send is libnotify's, which is the one a Linux desktop has.
		return "notify-send", []string{title, body}
	}
}

// appleScriptString renders s as an AppleScript string literal. A backslash or
// a double quote would otherwise end the literal or start an escape of its own.
func appleScriptString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		if r == '\\' || r == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
