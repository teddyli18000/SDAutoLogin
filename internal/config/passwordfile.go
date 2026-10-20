package config

import (
	"bytes"
	"fmt"
	"strings"
)

// Where a password held in memory came from. It decides two things: whether the
// password is written back into config.json, and what the settings page is told.
const (
	// SourceConfig is a password stored in config.json.
	SourceConfig = "config"
	// SourceFile is a password read from the file PasswordFile names. What is
	// read from a file is never written back into config.json.
	SourceFile = "file"
	// SourceRun is a password given to this run alone, by --password or by
	// --account. --save writes it down for the runs that follow.
	SourceRun = "run"
)

// passwordEntry is one "account:password" line of a password file.
type passwordEntry struct {
	name   string
	secret string
}

// splitLines splits a password file into lines. The UTF-8 byte order mark a Windows
// editor writes at the front of the file is dropped, and a carriage return ends a
// line as well: a file written on Windows ends its lines with CRLF, and the same file
// saved by another program may end them with a CR alone. A CR left inside a line
// would put the rest of the file into that line's password.
func splitLines(data []byte) []string {
	text := string(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}))
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		// A trailing newline leaves no line behind.
		lines = lines[:n-1]
	}
	return lines
}

// passwordFileShape is what a password file's lines look like before anything is
// guessed from them: how many lines carry something, and whether a colon makes any of
// them an account:password line. One line without a colon is one password; several
// lines of that shape are not something to guess at, which is what the caller decides
// with this.
type passwordFileShape struct {
	lines int
	named bool
}

func describePasswordFile(data []byte) passwordFileShape {
	shape := passwordFileShape{}
	for _, line := range splitLines(data) {
		if line == "" {
			continue
		}
		shape.lines++
		if strings.Contains(line, ":") {
			shape.named = true
		}
	}
	return shape
}

// parsePasswordFile reads a password file. No colon on any line makes it one
// secret for the first entry, the shape a service handed a single credential
// writes. A colon makes it "account:password" lines, and an unused name is
// reported.
func parsePasswordFile(data []byte) (list []passwordEntry, single string, err error) {
	lines := splitLines(data)

	named := false
	for _, l := range lines {
		if strings.Contains(l, ":") {
			named = true
			break
		}
	}
	if !named {
		// The first line stands as written: only the line ending is dropped,
		// since a password may end in a space.
		if len(lines) == 0 || lines[0] == "" {
			return nil, "", fmt.Errorf("holds no password")
		}
		return nil, lines[0], nil
	}

	seen := make(map[string]bool, len(lines))
	for i, l := range lines {
		if l == "" {
			continue
		}
		name, secret, ok := strings.Cut(l, ":")
		if !ok {
			return nil, "", fmt.Errorf("line %d holds no colon while the others name an account", i+1)
		}
		name = strings.TrimSpace(name)
		switch {
		case name == "":
			return nil, "", fmt.Errorf("line %d names no account", i+1)
		case secret == "":
			return nil, "", fmt.Errorf("line %d gives %s no password", i+1, name)
		case seen[name]:
			return nil, "", fmt.Errorf("line %d names %s a second time", i+1, name)
		}
		seen[name] = true
		list = append(list, passwordEntry{name: name, secret: secret})
	}
	if len(list) == 0 {
		return nil, "", fmt.Errorf("holds no password")
	}
	return list, "", nil
}
