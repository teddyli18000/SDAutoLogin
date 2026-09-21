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
	// mainPkg is the entry point package. Everything else is a library under
	// internal/, so the build has to name it explicitly.
	mainPkg = "./cmd/SDAutoLogin"
	// distDir holds everything the build produces, so the repository root stays
	// free of executables and the results of every platform sit together.
	distDir = "dist"
	// The generated resources belong to the winres package, next to the files
	// they are made from. The linker only picks up a .syso that lies in one of
	// the packages being built, which is why these are not under distDir.
	iconPath  = "winres/icon.ico"
	resPrefix = "winres/rsrc"
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
	"clean":     {"remove the built executables, keeping settings and logs", taskClean},
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
// and the Linux one for a server, which also serves the command line. The 64 bit
// builds come first in each group, the 32 bit and small machine ones last.
func taskAll(root string) error {
	// goarm, where it is set, is the instruction set a 32 bit ARM build is for.
	// GOARCH only says arm, and which of the versions a board needs is the
	// difference between a program that runs there and one that does not, so it
	// is passed in the environment and it is part of the file name, since a
	// downloader has to be able to tell those files apart as well.
	targets := []struct {
		goos, goarch, goarm string
	}{
		{"windows", "amd64", ""},
		{"windows", "arm64", ""},
		{"windows", "386", ""},
		{"linux", "amd64", ""},
		{"linux", "arm64", ""},
		{"linux", "386", ""},
		{"linux", "arm", "7"},
	}
	for _, t := range targets {
		env := []string{"GOOS=" + t.goos, "GOARCH=" + t.goarch, "CGO_ENABLED=0"}
		arch := t.goarch
		if t.goarm != "" {
			env = append(env, "GOARM="+t.goarm)
			arch += "v" + t.goarm
		}
		name := appName(t.goos, "-"+t.goos+"-"+arch)
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
	if err := runGo(root, env, "build", "-trimpath", "-ldflags", flags, "-o", out, mainPkg); err != nil {
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
	//
	// One file per architecture, named rsrc_windows_<arch>.syso, which is also
	// how the toolchain knows which one belongs to which build. Every Windows
	// architecture that is built needs its own, or that build would be the one
	// without an icon and version information.
	env := []string{"GOFLAGS=-mod=mod"}
	return runGo(root, env, "run", winresVersion, "make",
		"--in", resConfig, "--out", resPrefix, "--arch", "amd64,arm64,386")
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

// taskClean removes the built executables. The settings and the log live in the
// same directory, next to the executable, so they are left alone: cleaning a
// build must not throw away an account.
func taskClean(root string) error {
	dir := filepath.Join(root, distDir)
	matches, err := filepath.Glob(filepath.Join(dir, "SDAutoLogin*"))
	if err != nil {
		return err
	}
	removed := 0
	for _, m := range matches {
		if info, err := os.Stat(m); err != nil || info.IsDir() {
			continue
		}
		if err := os.Remove(m); err != nil {
			return err
		}
		removed++
	}
	fmt.Printf("  removed %d executable(s) from %s\n", removed, describe(root, dir))
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
