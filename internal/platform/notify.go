package platform

import (
	"strings"
	"unicode"
)

// notificationTextLimit is how much of a message reaches a notification. The text
// does not come from this program: the body carries a message a portal on the
// network sent back, read out of an HTTP response.
const notificationTextLimit = 256

// notifyCommand is how a desktop is asked for a notification, or ("", nil) when
// there is no helper for it. It is kept apart from Notify, which each system
// provides for itself, so the command can be tested without running one.
//
// Both strings are folded into one line of printable characters first. The body
// is text a portal on the network chose, so it may hold newlines, control
// characters and any amount of padding, and neither shape is one a notification
// can carry: a newline ends an AppleScript string literal and loses the
// notification altogether, and the rest is a stranger writing on the desktop.
func notifyCommand(goos, title, body string) (string, []string) {
	title, body = oneLineText(title), oneLineText(body)
	switch goos {
	case "darwin":
		// osascript runs AppleScript source, so both strings go in as quoted
		// literals.
		return "osascript", []string{"-e",
			"display notification " + appleScriptString(body) +
				" with title " + appleScriptString(title)}
	default:
		// notify-send comes from libnotify and is on a Linux desktop.
		return "notify-send", []string{title, body}
	}
}

// oneLineText is what a notification may show of s: the printable characters on
// one line, with every control character and every run of spaces folded into a
// single space, and no more than notificationTextLimit characters, cut where a
// character ends rather than in the middle of one.
func oneLineText(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	spaced := false
	written := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			r = ' '
		}
		if r == ' ' {
			if spaced {
				continue
			}
			spaced = true
		} else {
			spaced = false
		}
		if written == notificationTextLimit {
			break
		}
		out.WriteRune(r)
		written++
	}
	return strings.TrimSpace(out.String())
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
