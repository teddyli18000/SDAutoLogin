package platform

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNotifyCommandFollowsTheSystem(t *testing.T) {
	cases := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{
			"darwin",
			"osascript",
			[]string{"-e", `display notification "the body" with title "the title"`},
		},
		{
			"linux",
			"notify-send",
			[]string{"the title", "the body"},
		},
	}

	for _, c := range cases {
		name, args := notifyCommand(c.goos, "the title", "the body")
		if name != c.wantName {
			t.Errorf("%s: command = %q, want %q", c.goos, name, c.wantName)
		}
		if !slices.Equal(args, c.wantArgs) {
			t.Errorf("%s: arguments = %q, want %q", c.goos, args, c.wantArgs)
		}
	}
}

func TestAppleScriptStringQuotesWhatWouldEndTheLiteral(t *testing.T) {
	// A quote would end the literal and a backslash start an escape, so both are
	// escaped; everything else, Chinese included, is left as it is.
	got := appleScriptString(`say "hi" \ 完成`)
	want := `"say \"hi\" \\ 完成"`
	if got != want {
		t.Errorf("appleScriptString = %s, want %s", got, want)
	}
}

// TestANotificationCarriesOneLineOfPrintableText covers the text that reaches a
// notification. The body is a message a portal sent back over plain HTTP, so it
// may hold newlines - which end an AppleScript string literal and lose the
// notification - control characters, and any amount of padding.
func TestANotificationCarriesOneLineOfPrintableText(t *testing.T) {
	name, args := notifyCommand("linux", "the\ttitle", "one\ntwo\r\nthree\x07four")
	if name != "notify-send" {
		t.Fatalf("command = %q, want notify-send", name)
	}
	if len(args) != 2 {
		t.Fatalf("arguments = %q, want a title and a body", args)
	}
	if args[0] != "the title" {
		t.Errorf("title = %q, want the tab folded into a space", args[0])
	}
	if args[1] != "one two three four" {
		t.Errorf("body = %q, want one line of printable text", args[1])
	}

	// The AppleScript branch is handed the same text, so a newline can no longer
	// turn the literal into a syntax error and lose the notification.
	if _, darwinArgs := notifyCommand("darwin", "t", "a\nb\"c\\d"); strings.ContainsAny(darwinArgs[1], "\n\r") {
		t.Errorf("the AppleScript source still holds a line break: %q", darwinArgs[1])
	}

	// A portal cannot pad the desktop with text of its own either, and the cut has
	// to fall where a character ends.
	if _, args := notifyCommand("linux", "t", strings.Repeat("x", 4000)); len(args[1]) != notificationTextLimit {
		t.Errorf("body = %d characters, want at most %d", len(args[1]), notificationTextLimit)
	}
	if _, args := notifyCommand("linux", "t", strings.Repeat("完", 4000)); !utf8.ValidString(args[1]) {
		t.Errorf("the text was cut in the middle of a character: %q", args[1])
	}
}
