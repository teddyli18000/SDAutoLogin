package platform

import (
	"os"
	"strings"
	"testing"
)

// TestTheAutostartCommandIsTheExecutableAndTheArguments covers what an entry
// starts: this program, named by its full path, and the arguments this run was
// told to repeat. Both halves matter. An entry does not run in any particular
// directory, so a bare name would start whatever the desktop finds first, and an
// entry with no arguments starts on a different configuration than the run that
// wrote it.
func TestTheAutostartCommandIsTheExecutableAndTheArguments(t *testing.T) {
	t.Cleanup(func() { SetAutostartArguments(nil) })

	exe, err := os.Executable()
	if err != nil || exe == "" {
		t.Skipf("this program cannot name its own executable: %v", err)
	}

	SetAutostartArguments(nil)
	if got, want := autostartCommand(), `"`+exe+`"`; got != want {
		t.Errorf("autostartCommand() = %q, want just the quoted executable %q", got, want)
	}

	const dir = `C:\Users\someone\My Settings`
	SetAutostartArguments([]string{"--home=" + dir})
	got := autostartCommand()
	if !strings.HasPrefix(got, `"`+exe+`" `) {
		t.Errorf("autostartCommand() = %q, want the executable first and quoted", got)
	}
	if want := `"--home=` + dir + `"`; !strings.Contains(got, want) {
		t.Errorf("autostartCommand() = %q, want the argument quoted, as %s", got, want)
	}

	// The list is what the macOS property list needs, where the arguments are
	// elements of their own rather than one line.
	list := autostartArgumentList()
	if len(list) != 1 || list[0] != "--home="+dir {
		t.Errorf("autostartArgumentList() = %q, want the one argument that was set", list)
	}
}

// TestAnArgumentIsQuotedOnlyWhereItNeedsToBe documents the other half of the
// command line: a value with a space in it has to be quoted or it becomes two
// arguments, and one without a space is left as it is, since an entry is read by
// a person as well.
func TestAnArgumentIsQuotedOnlyWhereItNeedsToBe(t *testing.T) {
	for _, c := range []struct{ arg, want string }{
		{`--home=C:\sda`, `--home=C:\sda`},
		{`--home=C:\a b`, `"--home=C:\a b"`},
		{"--home=C:\\a\tb", "\"--home=C:\\a\tb\""},
	} {
		if got := quoteArgument(c.arg); got != c.want {
			t.Errorf("quoteArgument(%q) = %q, want %q", c.arg, got, c.want)
		}
	}
}
