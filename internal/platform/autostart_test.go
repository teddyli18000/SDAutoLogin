//go:build !windows

package platform

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestTheAutostartCommandIsTheExecutableAndTheArguments covers what an entry
// starts: this program by its full path, an entry running in no particular
// directory, and the arguments this run repeats.
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

	// The list is what the macOS property list needs, where each argument is an
	// element of its own.
	list := autostartArgumentList()
	if len(list) != 1 || list[0] != "--home="+dir {
		t.Errorf("autostartArgumentList() = %q, want the one argument that was set", list)
	}
}

// TestAnArgumentIsQuotedOnlyWhereItNeedsToBe documents the other half of the
// command line: a value with a space has to be quoted or it becomes two arguments,
// and one without is left as it is, since a person reads the entry too.
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

// TestAnArgumentWithAQuoteIsEscaped covers the character that used to end the
// quoting: a value holding one handed the rest of the value to the program that
// starts the entry as another argument, and the backslashes in front of it were
// read as escapes of their own.
func TestAnArgumentWithAQuoteIsEscaped(t *testing.T) {
	for _, c := range []struct{ arg, want string }{
		{`--home=C:\sda`, `--home=C:\sda`},
		{`--home=C:\a b`, `"--home=C:\a b"`},
		{`--home=C:\a"b`, `"--home=C:\a\"b"`},
		{`--home=C:\a\"b`, `"--home=C:\a\\\"b"`},
		{`--home="`, `"--home=\""`},
		{"--home=a\tb", "\"--home=a\tb\""},
		{"", `""`},
	} {
		if got := quoteArgument(c.arg); got != c.want {
			t.Errorf("quoteArgument(%q) = %q, want %q", c.arg, got, c.want)
		}
	}
}

// TestTheDesktopEntryEscapesWhatADesktopParses covers the other command line a
// value goes into. A desktop reads the Exec key with rules of its own: inside a
// quoted argument a quote, a backtick, a dollar sign and a backslash are escaped
// with a backslash, and a percent sign is a field code wherever it stands, so a
// literal one has to be written as two - %i would otherwise arrive as an extra
// icon option and %u as an address of the launcher's own.
func TestTheDesktopEntryEscapesWhatADesktopParses(t *testing.T) {
	t.Cleanup(func() { SetAutostartArguments(nil) })

	for _, c := range []struct{ arg, want string }{
		{`--home=/home/u/.cache`, `--home=/home/u/.cache`},
		{`--home=/home/u/My Settings`, `"--home=/home/u/My Settings"`},
		{`--home=/home/u/a"b`, `"--home=/home/u/a\"b"`},
		{"--home=/home/u/a`b", "\"--home=/home/u/a\\`b\""},
		{`--home=/home/u/a$b`, `"--home=/home/u/a\$b"`},
		{`--home=/home/u/a\b`, `"--home=/home/u/a\\b"`},
		{`--home=/home/u/%i`, `--home=/home/u/%%i`},
		{`--home=/home/u/50%`, `--home=/home/u/50%%`},
		{"", `""`},
	} {
		if got := desktopQuote(c.arg); got != c.want {
			t.Errorf("desktopQuote(%q) = %q, want %q", c.arg, got, c.want)
		}
	}

	exe, err := os.Executable()
	if err != nil || exe == "" {
		t.Skipf("this program cannot name its own executable: %v", err)
	}
	SetAutostartArguments([]string{`--home=/home/u/My Settings`})
	want := desktopQuote(exe) + ` "--home=/home/u/My Settings"`
	if got := desktopCommand(); got != want {
		t.Errorf("desktopCommand() = %q, want %q", got, want)
	}
	if entry := linuxDesktopEntry(); !strings.Contains(entry, "Exec="+want+"\n") {
		t.Errorf("linuxDesktopEntry() = %q, want it to hold Exec=%s", entry, want)
	}
}

// TestAHelperIsLookedForWhereItIsInstalled covers the order a helper program is
// looked for in: the place the system installs it comes before PATH, so a
// directory earlier on PATH does not get to decide which program runs, and a
// system that installs the helper elsewhere is still served by its bare name.
func TestAHelperIsLookedForWhereItIsInstalled(t *testing.T) {
	installed := filepath.Join(t.TempDir(), "notify-send")
	elsewhereDir := t.TempDir()
	elsewhere := filepath.Join(elsewhereDir, "notify-send")
	for _, path := range []string{installed, elsewhere} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", elsewhereDir)

	cmd, path := externalCommand([]string{installed, "notify-send"}, "title", "body")
	if cmd == nil || path != installed {
		t.Fatalf("externalCommand chose %q, want the installed path %q", path, installed)
	}
	if want := []string{installed, "title", "body"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("the command was built as %q, want %q", cmd.Args, want)
	}

	cmd, path = externalCommand([]string{filepath.Join(elsewhereDir, "gone"), "notify-send"})
	if cmd == nil || path != elsewhere {
		t.Fatalf("externalCommand chose %q, want the program PATH names (%q)", path, elsewhere)
	}
	if cmd.Args[0] != elsewhere {
		t.Errorf("the command was built with %q, want the resolved path %q", cmd.Args[0], elsewhere)
	}

	if cmd, path := externalCommand([]string{"/nonexistent/helper", "no-such-helper-anywhere"}); cmd != nil {
		t.Errorf("externalCommand = %v (%q), want nothing for a helper that is not installed", cmd, path)
	}
}

// TestABrowserSettingIsRunAsTheProgramItNames covers $BROWSER, which is the
// user's own setting and has to stay usable: a program named on PATH is looked up
// and the path that was resolved is the one that is run, a value holding a path
// has to be an absolute one, and anything else is left to the desktop's own
// handler rather than run.
func TestABrowserSettingIsRunAsTheProgramItNames(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "my-browser")
	handler := filepath.Join(dir, "xdg-open")
	for _, path := range []string{program, handler} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)

	const target = "http://127.0.0.1:1/?token=tok"
	cmd, path := browserCommand("my-browser", target)
	if cmd == nil || path != program {
		t.Fatalf("browserCommand chose %q, want the program PATH names (%q)", path, program)
	}
	if want := []string{program, target}; !slices.Equal(cmd.Args, want) {
		t.Errorf("the command was built as %q, want %q", cmd.Args, want)
	}

	if cmd, path := browserCommand(program, target); cmd == nil || path != program {
		t.Errorf("browserCommand(%q) = %q, want the absolute path itself", program, path)
	}

	// A relative path would be resolved against whatever directory this run
	// happens to stand in, so it is not run at all. Neither is anything else that
	// is not a program: an option written after the name, a name that is nowhere,
	// a path that does not exist.
	for _, browser := range []string{"./my-browser", "nowhere/my-browser", "no-such-browser", "my-browser --new-window"} {
		if cmd, path := browserCommand(browser, target); cmd != nil {
			t.Errorf("browserCommand(%q) ran %q, want it left to the desktop's handler", browser, path)
		}
	}

	if runtime.GOOS == "darwin" {
		t.Skip("the launcher of macOS is the one command this branch builds")
	}
	t.Setenv("BROWSER", "my-browser")
	if cmd, path := openCommand(target); cmd == nil || path != program {
		t.Errorf("openCommand chose %q, want $BROWSER (%q)", path, program)
	}
	// A $BROWSER this program cannot run leaves the address to the desktop's own
	// handler, and one that is not set uses it straight away.
	for _, browser := range []string{"./my-browser", ""} {
		t.Setenv("BROWSER", browser)
		cmd, path := openCommand(target)
		if cmd == nil {
			t.Fatalf("openCommand found nothing to open the address with for $BROWSER=%q", browser)
		}
		if filepath.Base(path) == "my-browser" && path != handler {
			t.Errorf("openCommand ran the $BROWSER value it could not use: %q", path)
		}
	}
}

// TestTheAutostartEntryIsWrittenAndFoundAndRemoved covers the round trip the switch in
// the menu depends on: an entry written where the desktop reads it, found again by the
// check the menu is drawn from, and removed when it is turned off. It is written into a
// home directory of this test's own, so nothing of the machine running it is touched.
func TestTheAutostartEntryIsWrittenAndFoundAndRemoved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	exe, err := os.Executable()
	if err != nil || exe == "" {
		t.Skipf("this program cannot name its own executable: %v", err)
	}
	path := autostartPath()
	if path == "" {
		t.Fatal("a home directory of this test's own was not read as one")
	}
	if AutostartEnabled() {
		t.Fatal("an autostart entry was found before one was written")
	}

	if !SetAutostart(true) {
		t.Fatal("writing an autostart entry into a home directory of this test's own failed")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no autostart entry was written where the desktop reads it: %v", err)
	}
	// The entry has to start this program: an entry that is there and starts nothing
	// is worse than none, since the switch in the menu reads it as done.
	if runtime.GOOS != "darwin" {
		if !strings.Contains(string(body), "Exec=") || !strings.Contains(string(body), exe) {
			t.Errorf("the desktop entry does not start this program: %q", body)
		}
	}
	if !AutostartEnabled() {
		t.Error("the autostart entry that was just written is not found")
	}

	if !SetAutostart(false) {
		t.Fatal("removing the autostart entry failed")
	}
	if AutostartEnabled() {
		t.Error("the autostart entry is still found after it was removed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the autostart entry is still on disk after it was removed: %v", err)
	}
	// Turning it off again is not a failure: an entry that is already gone is the
	// state the switch was asked for.
	if !SetAutostart(false) {
		t.Error("turning autostart off a second time failed")
	}
}

// TestWithNoHomeDirectoryThereIsNowhereToPutAnEntry covers the machine that cannot keep
// an entry. A path built without a home directory would be relative, landing the entry
// where the desktop never looks, so nothing is written and nothing is claimed to be
// there.
func TestWithNoHomeDirectoryThereIsNowhereToPutAnEntry(t *testing.T) {
	t.Setenv("HOME", "")

	if got := autostartPath(); got != "" {
		t.Errorf("autostartPath() = %q with no home directory, want nothing", got)
	}
	if AutostartEnabled() {
		t.Error("an autostart entry was found on a machine with no home directory")
	}
	if SetAutostart(true) {
		t.Error("an autostart entry was reported as written with no home directory to keep it in")
	}
	if SetAutostart(false) {
		t.Error("removing an autostart entry that cannot exist was reported as done")
	}
}

// TestTheLaunchAgentIsWellFormedWhateverTheArgumentsHold covers the property list macOS
// reads: it is XML, so a path or an argument holding & or < would make the file invalid,
// leaving an entry that is there and starts nothing. The list is parsed here the way the
// system parses it, so an escape that was dropped is a parse error rather than a detail
// no one notices.
func TestTheLaunchAgentIsWellFormedWhateverTheArgumentsHold(t *testing.T) {
	t.Cleanup(func() { SetAutostartArguments(nil) })
	arguments := []string{"--home", "/tmp/a & b/<settings>", `a "quoted" \ path`, "it's here"}
	SetAutostartArguments(arguments)

	exe, err := os.Executable()
	if err != nil || exe == "" {
		t.Skipf("this program cannot name its own executable: %v", err)
	}

	var plist struct {
		Arguments []string `xml:"dict>array>string"`
	}
	if err := xml.Unmarshal([]byte(macPlist()), &plist); err != nil {
		t.Fatalf("the launch agent is not XML: %v\n%s", err, macPlist())
	}
	if want := append([]string{exe}, arguments...); !slices.Equal(plist.Arguments, want) {
		t.Errorf("the launch agent starts with %q, want %q", plist.Arguments, want)
	}
}
