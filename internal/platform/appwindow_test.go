package platform

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

func TestAppWindowCommandAsksTheBrowserForAWindowOfItsOwn(t *testing.T) {
	const target = "http://127.0.0.1:8088/?token=tok"

	cases := []struct {
		goos     string
		browser  string
		wantName string
		wantArgs []string
	}{
		{
			"windows",
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			[]string{"--app=" + target},
		},
		{
			"linux",
			"/usr/bin/chromium",
			"/usr/bin/chromium",
			[]string{"--app=" + target},
		},
		{
			// macOS starts the bundle through the launcher and passes the flag
			// on, bringing up the same kind of window.
			"darwin",
			"/Applications/Google Chrome.app",
			// The launcher by its path: a bare name is one a directory earlier
			// on PATH could answer with a program of its own.
			"/usr/bin/open",
			[]string{"-na", "/Applications/Google Chrome.app", "--args", "--app=" + target},
		},
	}

	for _, c := range cases {
		name, args := appWindowCommand(c.goos, c.browser, target)
		if name != c.wantName {
			t.Errorf("%s: command = %q, want %q", c.goos, name, c.wantName)
		}
		if !slices.Equal(args, c.wantArgs) {
			t.Errorf("%s: arguments = %q, want %q", c.goos, args, c.wantArgs)
		}
	}
}

func TestFindAppBrowserTakesTheFirstOneThatIsThere(t *testing.T) {
	// The path has to be absolute for the system the tests run on, since that
	// decides whether a candidate is looked for on the file system or on PATH.
	installed := filepath.Join(t.TempDir(), "chrome")
	candidates := []string{installed, "chromium", "microsoft-edge"}

	isFile := func(present ...string) func(string) bool {
		return func(path string) bool { return slices.Contains(present, path) }
	}
	onPath := func(present ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			if slices.Contains(present, name) {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		}
	}

	cases := []struct {
		what string
		file func(string) bool
		path func(string) (string, error)
		want string
	}{
		{"none of them is installed", isFile(), onPath(), ""},
		{"the first one is installed", isFile(installed), onPath(), installed},
		{"only the second one is installed", isFile(), onPath("chromium"), "/usr/bin/chromium"},
		{"only the last one is installed", isFile(), onPath("microsoft-edge"), "/usr/bin/microsoft-edge"},
		{
			// The candidate list decides, whatever order they turn up in.
			"several are installed",
			isFile(installed),
			onPath("chromium", "microsoft-edge"),
			installed,
		},
		{
			"a path is not looked for on PATH",
			isFile(),
			onPath(installed),
			"",
		},
	}

	for _, c := range cases {
		if got := findAppBrowser(candidates, c.file, c.path); got != c.want {
			t.Errorf("%s: findAppBrowser = %q, want %q", c.what, got, c.want)
		}
	}
}

func TestAppBrowserCandidatesFollowTheSystem(t *testing.T) {
	chromeRel := filepath.Join("Google", "Chrome", "Application", "chrome.exe")
	edgeRel := filepath.Join("Microsoft", "Edge", "Application", "msedge.exe")
	programFiles := `C:\Program Files`
	programFilesX86 := `C:\Program Files (x86)`

	env := func(name string) string {
		switch name {
		case "ProgramFiles":
			return programFiles
		case "ProgramFiles(x86)":
			return programFilesX86
		}
		return ""
	}

	windows := appBrowserCandidates("windows", env)
	want := []string{
		filepath.Join(programFiles, chromeRel),
		filepath.Join(programFilesX86, chromeRel),
		filepath.Join(programFiles, edgeRel),
		filepath.Join(programFilesX86, edgeRel),
	}
	if !slices.Equal(windows, want) {
		t.Errorf("windows candidates = %q, want %q", windows, want)
	}

	// Without the environment there is no directory to look in, so no path is
	// built out of empty parts.
	if got := appBrowserCandidates("windows", func(string) string { return "" }); len(got) != 0 {
		t.Errorf("windows candidates without an environment = %q, want none", got)
	}

	darwin := appBrowserCandidates("darwin", env)
	for _, want := range []string{"/Applications/Google Chrome.app", "/Applications/Microsoft Edge.app"} {
		if !slices.Contains(darwin, want) {
			t.Errorf("darwin candidates = %q, want %q among them", darwin, want)
		}
	}
	for _, c := range darwin {
		if filepath.Ext(c) != ".app" {
			t.Errorf("darwin candidate %q is not an application bundle", c)
		}
	}

	linux := appBrowserCandidates("linux", env)
	for _, want := range []string{"google-chrome", "chromium", "microsoft-edge"} {
		if !slices.Contains(linux, want) {
			t.Errorf("linux candidates = %q, want %q among them", linux, want)
		}
	}
	// These are looked up on PATH, so a path here would be skipped by the file
	// system branch of findAppBrowser and quietly never found.
	for _, c := range linux {
		if filepath.IsAbs(c) {
			t.Errorf("linux candidate %q is a path, want a name to look up on PATH", c)
		}
	}
}
