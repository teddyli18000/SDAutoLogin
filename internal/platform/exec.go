package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sdautologin/internal/logging"
)

// executablePath is the command that starts this program, quoted so a path with
// a space survives; an autostart entry stores it. The result is empty when the
// program cannot name its own executable, and callers then write nothing down.
func executablePath() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return ""
	}
	return `"` + exe + `"`
}

// autostartArguments are the arguments an autostart entry has to repeat from the
// run that wrote it. They are recorded once at startup, since an entry is written
// later, from the tray or from the settings page.
var autostartArguments []string

// SetAutostartArguments records the arguments an autostart entry repeats, so a
// run given --home keeps its own settings and logs when it starts. Nothing
// secret belongs here: the entry outlives the run and stays readable.
func SetAutostartArguments(args []string) { autostartArguments = args }

// autostartCommand is the command an autostart entry stores.
func autostartCommand() string {
	out := executablePath()
	for _, arg := range autostartArguments {
		out += " " + quoteArgument(arg)
	}
	return out
}

// autostartArgumentList is the same arguments one by one, the shape a macOS
// property list wants.
func autostartArgumentList() []string { return autostartArguments }

// externalCommand builds the command for a helper program, given the names it is
// known by, the places it is installed in first. An absolute path is tried
// before any name is looked for on PATH, so a directory someone put earlier on
// PATH does not get to decide which program runs; a bare name is the fallback
// for a system that installs the helper elsewhere. The name is resolved here
// rather than left to exec.Command so that the path that was chosen is the one
// that is used, and so that a helper that is nowhere is a nil command the caller
// can report rather than a start that fails at the last moment.
func externalCommand(names []string, args ...string) (*exec.Cmd, string) {
	for _, name := range names {
		if filepath.IsAbs(name) && isFile(name) {
			return exec.Command(name, args...), name
		}
	}
	for _, name := range names {
		if filepath.IsAbs(name) {
			continue
		}
		if found, err := exec.LookPath(name); err == nil {
			return exec.Command(found, args...), found
		}
	}
	return nil, ""
}

// browserCommand is the command a $BROWSER value asks for. The value is the
// user's own setting and stays usable: a value that names a program on PATH is
// looked up, and one that holds a separator has to be the absolute path of one,
// since a relative path would be resolved against whatever directory this run
// happens to stand in - an entry of "browser" would run a file of that name in
// it. Nothing further is done with the value: an option written after a space, a
// %s the setting is documented to take, or a colon-separated list of browsers is
// not a shape that can be run as one command, and splitting it up would be
// guessing.
func browserCommand(browser, target string) (*exec.Cmd, string) {
	if strings.ContainsRune(browser, filepath.Separator) {
		if !filepath.IsAbs(browser) || !isFile(browser) {
			logging.Log.Warn("$BROWSER names %q, which is not the absolute path of a program; the desktop's own handler is used instead", browser)
			return nil, ""
		}
		return exec.Command(browser, target), browser
	}
	found, err := exec.LookPath(browser)
	if err != nil {
		logging.Log.Warn("$BROWSER names %q, which is not a program that can be found; the desktop's own handler is used instead", browser)
		return nil, ""
	}
	return exec.Command(found, target), found
}

// quoteArgument quotes an argument that holds a space or a double quote, since
// it has to survive being read back as one argument by the program that starts
// the entry. A double quote inside the value would otherwise end the quoting and
// hand the rest of the value to that program as another argument.
//
// The rule followed here is the one a Windows command line is read by, since the
// registry value of the autostart entry is a command line Windows parses:
// backslashes are part of the value, and only a run of them in front of a quote
// decides whether that quote ends the argument. A quote therefore takes one
// backslash, and every backslash of the run in front of it is doubled.
func quoteArgument(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\"") {
		return arg
	}
	var out strings.Builder
	out.Grow(len(arg) + 2)
	out.WriteByte('"')
	for i := 0; i < len(arg); {
		if arg[i] != '\\' {
			if arg[i] == '"' {
				out.WriteByte('\\')
			}
			out.WriteByte(arg[i])
			i++
			continue
		}
		run := i
		for run < len(arg) && arg[run] == '\\' {
			run++
		}
		if run < len(arg) && arg[run] == '"' {
			out.WriteString(strings.Repeat(`\`, 2*(run-i)+1))
			out.WriteByte('"')
			i = run + 1
			continue
		}
		out.WriteString(arg[i:run])
		i = run
	}
	out.WriteByte('"')
	return out.String()
}
