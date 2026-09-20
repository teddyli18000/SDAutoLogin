// Command build is the task runner for this repository. Every build flag lives
// here, so the wrapper scripts and the CMake targets call these tasks instead of
// repeating a command line and drifting apart.
//
//	go run ./tools/build            list the tasks
//	go run ./tools/build build      build for this platform
//
// It only uses the standard library, so it works offline next to the vendored
// dependencies, and it behaves the same on Windows, Linux and macOS.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	distDir  = "dist"
	iconPath = "winres/icon.ico"
	// go-winres appends the platform to the output prefix, so the prefix below
	// produces rsrc_windows_amd64.syso, the file the linker picks up.
	resPrefix = "rsrc"
	resConfig = "winres/winres.json"
	// winresVersion is pinned so regenerating the resources is reproducible.
	winresVersion = "github.com/tc-hib/go-winres@v0.3.3"
)

type task struct {
	summary string
	run     func(root string) error
}

// taskOrder keeps the listing stable, a map is not ordered.
var taskOrder = []string{"build", "console", "all", "icon", "resources", "test", "run", "clean"}

var tasks = map[string]task{
	"build":     {"build for this platform into dist/", taskBuild},
	"console":   {"build the console variant of this platform into dist/", taskConsole},
	"all":       {"cross compile the Windows and Linux builds into dist/", taskAll},
	"icon":      {"redraw winres/icon.ico from the drawing code", taskIcon},
	"resources": {"regenerate the Windows resource file with go-winres", taskResources},
	"test":      {"run go vet and go test", taskTest},
	"run":       {"build and start the program", taskRun},
	"clean":     {"remove the build output", taskClean},
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.Exit(1)
	}

	name := ""
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	if name == "" || name == "help" || name == "-h" || name == "--help" {
		usage()
		return
	}
	t, ok := tasks[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "build: unknown task %q\n\n", name)
		usage()
		os.Exit(2)
	}

	fmt.Printf("== %s: %s\n", name, t.summary)
	if err := t.run(root); err != nil {
		fmt.Fprintf(os.Stderr, "== %s failed: %v\n", name, err)
		os.Exit(1)
	}
	fmt.Printf("== %s done\n", name)
}

func usage() {
	fmt.Print("Usage: go run ./tools/build [task]\n\nTasks:\n")
	for _, name := range taskOrder {
		fmt.Printf("  %-10s %s\n", name, tasks[name].summary)
	}
	fmt.Print("\nPaths:\n" +
		"  output   " + distDir + "/\n" +
		"  icon     " + iconPath + "\n" +
		"  resource " + resPrefix + "_<platform>.syso\n")
}

// repoRoot walks up from the working directory until it finds go.mod, so the
// tasks also work from a build directory such as the one CMake creates.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}

// appName is the file name of the executable for a target system, where suffix
// distinguishes the variants of one platform.
func appName(goos, suffix string) string {
	name := "SDAutoLogin" + suffix
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func taskBuild(root string) error {
	return buildFor(root, runtime.GOOS, runtime.GOARCH, appName(runtime.GOOS, ""), true, nil)
}

func taskConsole(root string) error {
	// Without -H=windowsgui the program keeps the console it was started from,
	// which is what a script or a service wants. On the other systems the
	// executable is a console program either way, so this is the same build.
	return buildFor(root, runtime.GOOS, runtime.GOARCH, appName(runtime.GOOS, "-console"), false, nil)
}

// taskAll builds the binaries worth publishing: the Windows one to double click
// and the Linux one for a server, which also serves the command line.
func taskAll(root string) error {
	targets := []struct{ goos, goarch string }{
		{"windows", "amd64"},
		{"windows", "arm64"},
		{"linux", "amd64"},
		{"linux", "arm64"},
	}
	for _, t := range targets {
		env := []string{"GOOS=" + t.goos, "GOARCH=" + t.goarch, "CGO_ENABLED=0"}
		name := appName(t.goos, "-"+t.goos+"-"+t.goarch)
		if err := buildFor(root, t.goos, t.goarch, name, true, env); err != nil {
			return err
		}
	}
	// The macOS tray needs cgo, so it cannot be cross compiled from here.
	if runtime.GOOS == "darwin" {
		return buildFor(root, "darwin", runtime.GOARCH, appName("darwin", "-darwin-"+runtime.GOARCH), true, nil)
	}
	fmt.Println("  note: the macOS build needs cgo, build it on macOS")
	return nil
}

func buildFor(root, goos, goarch, name string, gui bool, env []string) error {
	flags := "-s -w"
	if gui && goos == "windows" {
		flags += " -H=windowsgui"
	}
	out := filepath.Join(root, distDir, name)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := runGo(root, env, "build", "-trimpath", "-ldflags", flags, "-o", out, "."); err != nil {
		return err
	}
	fmt.Printf("  -> %s\n", describe(root, out))
	return nil
}

func taskIcon(root string) error {
	// The generator carries a build tag that keeps it out of the normal build,
	// so it is run by file name.
	return runGo(root, nil, "run", "tools/icogen/main.go", iconPath)
}

func taskResources(root string) error {
	// go-winres is not a dependency of the program, it only turns the icon and
	// the version information into an object file. Running it by module path
	// keeps it out of go.mod, which needs the module mode: the vendor directory
	// cannot resolve a package that is not a dependency.
	env := []string{"GOFLAGS=-mod=mod"}
	return runGo(root, env, "run", winresVersion, "make",
		"--in", resConfig, "--out", resPrefix, "--arch", "amd64")
}

func taskTest(root string) error {
	if err := runGo(root, nil, "vet", "./..."); err != nil {
		return err
	}
	return runGo(root, nil, "test", "./...")
}

func taskRun(root string) error {
	if err := taskBuild(root); err != nil {
		return err
	}
	exe := filepath.Join(root, distDir, appName(runtime.GOOS, ""))
	cmd := exec.Command(exe)
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func taskClean(root string) error {
	path := filepath.Join(root, distDir)
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	fmt.Println("  removed " + path)
	return nil
}

// runGo runs the go tool from the repository root. Cross compiling is expressed
// in the environment, which go reads as the target platform.
func runGo(root string, env []string, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	fmt.Printf("  %s\n", strings.TrimSpace(strings.Join(env, " ")+" go "+quoteArgs(args)))
	return cmd.Run()
}

// quoteArgs renders a command line the way it would be typed, so a flag that
// carries spaces is readable and can be copied.
func quoteArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t") {
			a = `"` + a + `"`
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

// describe renders a path relative to the repository when it is inside it, plus
// its size, which is the interesting part of a release build.
func describe(root, path string) string {
	shown := path
	if rel, err := filepath.Rel(root, path); err == nil {
		shown = rel
	}
	info, err := os.Stat(path)
	if err != nil {
		return shown
	}
	return fmt.Sprintf("%s (%.1f MiB)", shown, float64(info.Size())/(1<<20))
}
