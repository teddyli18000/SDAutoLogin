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
	// progName is what the program is called, which is the name of every file
	// the build writes and of every package it makes.
	progName = "SDAutoLogin"
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

	// notrayTag is the build tag of the builds that leave the tray out, which
	// is the one every package is built from: there is no desktop session on a
	// router, in a container or on a server to put an icon in.
	notrayTag = "notray"
)

type task struct {
	summary string
	run     func(root string) error
}

// taskOrder keeps the listing stable, a map is not ordered.
var taskOrder = []string{"build", "console", "all", "packages", "verify", "windows", "openwrt", "deb", "rpm", "pacman", "icon", "resources", "test", "run", "clean"}

var tasks = map[string]task{
	"build":     {"build for this platform into dist/", taskBuild},
	"console":   {"build the console variant of this platform into dist/", taskConsole},
	"all":       {"cross compile the Windows and Linux builds into dist/", taskAll},
	"packages":  {"pack every format into dist/packages/", taskPackages},
	"verify":    {"check every package with its own tool, and pack again", taskVerify},
	"windows":   {"pack the Windows builds and their install manifests", taskWindows},
	"openwrt":   {"pack the MIPS builds for OpenWrt into dist/packages/", taskOpenwrt},
	"deb":       {"pack the Debian packages into dist/packages/", taskDeb},
	"rpm":       {"pack the RPM packages into dist/packages/", taskRpm},
	"pacman":    {"pack the Arch packages into dist/packages/", taskPacman},
	"icon":      {"redraw winres/icon.ico from the drawing code", taskIcon},
	"resources": {"regenerate the Windows resource file with go-winres", taskResources},
	"test":      {"run go vet and go test", taskTest},
	"run":       {"build and start the program", taskRun},
	"clean":     {"remove the built executables, keeping settings and logs", taskClean},
}

// taskPackages is every format this repository publishes, which is what a release
// needs. Each format builds the binaries it wants for itself, and the ones that
// another format already built come out of the cache of the toolchain.
//
// The list is spelled out rather than read from the task map, which would be a
// cycle: the map holds this function.
func taskPackages(root string) error {
	formats := []struct {
		name string
		run  func(string) error
	}{
		{"windows", taskWindows},
		{"openwrt", taskOpenwrt},
		{"deb", taskDeb},
		{"rpm", taskRpm},
		{"pacman", taskPacman},
	}
	for _, f := range formats {
		fmt.Printf("-- %s\n", f.name)
		if err := f.run(root); err != nil {
			return err
		}
	}
	// The digests of everything that was just written go next to it, as the one
	// file that says what a release contains.
	return writeChecksums(root)
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
		"  packages " + distDir + "/" + pkgDir + "/\n" +
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
	name := progName + suffix
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

// target is one of the builds worth publishing.
type target struct {
	goos, goarch string
	// variant is how the file name spells the architecture, for the targets
	// where GOARCH is not precise enough to be read on its own.
	variant string
	// extraEnv is what goes into the environment on top of GOOS and GOARCH, for
	// the targets where GOARCH is not the whole story: arm does not say which
	// instruction set it is for, and mips does not say whether the board has a
	// floating point unit.
	extraEnv []string
	// tags are the build tags to build with, empty for none.
	tags string
}

// archName is the architecture as the file name spells it.
func (t target) archName() string {
	if t.variant != "" {
		return t.variant
	}
	return t.goarch
}

// name is the file name this target is built to, inside dist/.
func (t target) name() string {
	return appName(t.goos, "-"+t.goos+"-"+t.archName())
}

// env is the environment this target is built in, on top of the process one.
func (t target) env() []string {
	return append([]string{"GOOS=" + t.goos, "GOARCH=" + t.goarch, "CGO_ENABLED=0"}, t.extraEnv...)
}

// buildArgs are what go build needs for this target beyond the arguments
// buildFor always passes.
func (t target) buildArgs() []string {
	if t.tags == "" {
		return nil
	}
	return []string{"-tags", t.tags}
}

// buildTarget builds one target into dist/.
func buildTarget(root string, t target) error {
	return buildFor(root, t.goos, t.goarch, t.name(), true, t.env(), t.buildArgs()...)
}

// targets is every build worth publishing: the Windows one to double click, the
// Linux one for a server, which also serves the command line, and the small ones
// that a router or an embedded board has room for. The 64 bit builds come first
// in each group, the small machine ones last.
//
// Every Linux build is made twice: the second one leaves the tray out, for the
// machines that have no desktop session to put an icon in. A router never had a
// use for one, and the tray is by far the largest thing the binary can do
// without.
func targets() []target {
	base := []target{
		{goos: "windows", goarch: "amd64"},
		{goos: "windows", goarch: "arm64"},
		{goos: "windows", goarch: "386"},
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
		{goos: "linux", goarch: "386"},
		{goos: "linux", goarch: "arm", variant: "armv7", extraEnv: []string{"GOARM=7"}},
		// Most routers have no floating point unit, which is what softfloat is
		// for, and both byte orders are here because the boards are: the
		// Atheros targets (ath79, ar71xx) are big endian, the MediaTek ones
		// (ramips, mt76x8) are little endian.
		{goos: "linux", goarch: "mips", variant: "mips-softfloat", extraEnv: []string{"GOMIPS=softfloat"}},
		{goos: "linux", goarch: "mipsle", variant: "mipsle-softfloat", extraEnv: []string{"GOMIPS=softfloat"}},
	}

	all := make([]target, 0, 2*len(base))
	all = append(all, base...)
	for _, t := range base {
		if t.goos != "linux" {
			continue
		}
		t.variant = t.archName() + "-notray"
		t.tags = notrayTag
		all = append(all, t)
	}
	return all
}

// taskAll builds every target of the matrix into dist/.
func taskAll(root string) error {
	for _, t := range targets() {
		if err := buildTarget(root, t); err != nil {
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

// buildFor builds one target into dist/. moreArgs are extra arguments for go
// build, such as a build tag.
func buildFor(root, goos, goarch, name string, gui bool, env []string, moreArgs ...string) error {
	flags := "-s -w"
	if gui && goos == "windows" {
		flags += " -H=windowsgui"
	}
	out := filepath.Join(root, distDir, name)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	args := append([]string{"build", "-trimpath", "-ldflags", flags}, moreArgs...)
	args = append(args, "-o", out, mainPkg)
	if err := runGo(root, env, args...); err != nil {
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

	// The packages are made from those executables, so they go with them: one
	// left behind would be the previous build in a new one's clothes.
	packages := filepath.Join(dir, pkgDir)
	if _, err := os.Stat(packages); err == nil {
		shown := describe(root, packages)
		if err := os.RemoveAll(packages); err != nil {
			return err
		}
		fmt.Printf("  removed %s\n", shown)
	}
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
