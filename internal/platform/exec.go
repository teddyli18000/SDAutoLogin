package platform

import (
	"os"
	"strings"
)

// executablePath is the command that starts this program, quoted so it survives
// a path with spaces. It is what the autostart entry stores.
//
// It is empty when the program cannot name its own executable, which every caller
// treats as "there is no command to write down" rather than storing an entry that
// starts nothing.
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

// SetAutostartArguments records what an autostart entry has to pass on for the
// program it starts to be the program this run is: a run given --home keeps its
// settings and logs in that directory, and an entry without the option would start
// on a fresh configuration next to the executable.
//
// Nothing secret belongs here. An entry outlives the run, in a registry value or a
// file anyone can read.
func SetAutostartArguments(args []string) { autostartArguments = args }

// autostartCommand is what an entry stores as the command to run: the executable,
// and those arguments after it.
func autostartCommand() string {
	out := executablePath()
	for _, arg := range autostartArguments {
		out += " " + quoteArgument(arg)
	}
	return out
}

// autostartArgumentList is the same arguments one by one, which is the shape a
// macOS property list wants: it carries a list of arguments rather than a command
// line to be split.
func autostartArgumentList() []string { return autostartArguments }

// quoteArgument quotes an argument that holds a space, since it has to survive
// being read back as one word by the program that starts the entry.
func quoteArgument(arg string) string {
	if strings.ContainsAny(arg, " \t") {
		return `"` + arg + `"`
	}
	return arg
}
