package platform

import (
	"slices"
	"testing"
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
	// A quote would end the literal and a backslash would start an escape of
	// its own, so both are escaped; everything else, Chinese included, is left
	// as it is.
	got := appleScriptString(`say "hi" \ 完成`)
	want := `"say \"hi\" \\ 完成"`
	if got != want {
		t.Errorf("appleScriptString = %s, want %s", got, want)
	}
}
