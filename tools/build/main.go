// Command build is the task runner for this repository. Every build flag lives
// here, so the wrapper scripts and the CMake targets repeat no command line of
// their own. It uses the standard library only, so it builds offline the same
// way on Windows, Linux and macOS.
//
//	go run ./tools/build            list the tasks
//	go run ./tools/build build      build for this platform
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
	// progName is the program name, and the name of every file the build writes
	// and of every package it makes.
	progName = "SDAutoLogin"
	// mainPkg is the entry point package. Everything else is a library under
	// internal/, so the build has to name it explicitly.
	mainPkg = "./cmd/SDAutoLogin"
	// distDir holds everything the build produces, keeping executables out of the
	// repository root.
	distDir = "dist"
	// The generated resources belong to the winres package, next to the files they
	// are made from: the linker picks up a .syso only from a package being built,
	// so these are not under distDir.
	iconPath  = "winres/icon.ico"
	resPrefix = "winres/rsrc"
	resConfig = "winres/winres.json"
	// winresVersion is pinned so regenerating the resources is reproducible.
	winresVersion = "github.com/tc-hib/go-winres@v0.3.3"

	// versionVar and commitVar are the variables the linker writes the release
	// into. The path has to be the full one of the package holding them, and -X
	// cannot set a constant.
	versionVar = "sdautologin/internal/app.Version"
	commitVar  = "sdautologin/internal/app.Commit"
	// updaterVersionVar is the same version written where the updater can read
	// it: the updater holds a release against the version of the running program,
	// and it cannot read internal/app, which is the package that imports it. A
	// build that is not linked with this refuses to install anything, so a -X
	// that names nothing is a program that does not update itself rather than one
	// that updates without checking.
	updaterVersionVar = "sdautologin/internal/update.BuildVersion"

	// notrayTag is the build tag of the builds that leave the tray out. Every
	// package is built from one: a router, a container or a server has no desktop
	// session to put an icon in.
	notrayTag = "notray"
)

type task struct {
	summary string
	run     func(root string) error
}

// taskOrder keeps the listing stable, a map is not ordered.
var taskOrder = []string{"build", "console", "all", "packages", "verify", "windows", "openwrt", "deb", "rpm", "pacman", "icon", "resources", "version", "keygen", "sign", "test", "run", "clean"}

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
	"version":   {"print the version this tree builds, as every format spells it", taskVersion},
	"keygen":    {"make the key the releases are signed with", taskKeygen},
	"sign":      {"sign the checksum list of the packages just built", taskSign},
	"test":      {"vet this platform and Windows, then test under the race detector", taskTest},
	"run":       {"build and start the program", taskRun},
	"clean":     {"remove the built executables, keeping settings and logs", taskClean},
}

// taskPackages packs every format a release publishes. Each format builds the
// binaries it wants, and one another format already built comes out of the
// toolchain cache. The list is spelled out: the task map holds this function.
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
	// The digests of everything written here go next to it, as the one file that
	// says what a release contains.
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
	// as a script or a service wants. On the other systems the executable is a
	// console program either way, so this is the same build.
	return buildFor(root, runtime.GOOS, runtime.GOARCH, appName(runtime.GOOS, "-console"), false, nil)
}

// target is one of the builds this repository publishes.
type target struct {
	goos, goarch string
	// variant is how the file name spells the architecture where GOARCH is not
	// precise enough on its own.
	variant string
	// extraEnv goes into the environment on top of GOOS and GOARCH: arm does not
	// say which instruction set it is for, and mips does not say whether the board
	// has a floating point unit.
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

func buildTarget(root string, t target) error {
	return buildFor(root, t.goos, t.goarch, t.name(), true, t.env(), t.buildArgs()...)
}

// targets is every build this repository publishes: the Windows one to double
// click, the Linux one for a server and its command line, and the small ones a
// router or an embedded board has room for, the 64 bit builds first. Each Linux
// build is made twice, the second without the tray, by far the largest thing
// the binary can do without.
func targets() []target {
	base := []target{
		{goos: "windows", goarch: "amd64"},
		{goos: "windows", goarch: "arm64"},
		{goos: "windows", goarch: "386"},
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
		{goos: "linux", goarch: "386"},
		{goos: "linux", goarch: "arm", variant: "armv7", extraEnv: []string{"GOARM=7"}},
		// Most routers have no floating point unit, so these are softfloat
		// builds. Both byte orders are here because the boards are: Atheros
		// (ath79, ar71xx) is big endian, MediaTek (ramips, mt76x8) little endian.
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

// ldflagsFor is what the linker needs to write the release into the program:
// the version from the git tag the checkout is at, and the commit it was built
// from. The version is read here, since a program cannot read the tag it was
// built at.
func ldflagsFor(root string, gui bool, goos string) (string, error) {
	version, err := appVersion(root)
	if err != nil {
		return "", err
	}

	flags := "-s -w"
	if gui && goos == "windows" {
		flags += " -H=windowsgui"
	}
	flags += " -X " + versionVar + "=" + version
	flags += " -X " + updaterVersionVar + "=" + version
	if commit := appCommit(root); commit != "" {
		flags += " -X " + commitVar + "=" + commit
	}
	return flags, nil
}

// buildFor builds one target into dist/. moreArgs are extra arguments for go
// build, such as a build tag.
func buildFor(root, goos, goarch, name string, gui bool, env []string, moreArgs ...string) error {
	flags, err := ldflagsFor(root, gui, goos)
	if err != nil {
		return err
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

// winresEnv is the environment the resource generator runs in.
//
// go-winres is not a dependency of the program: it is not in go.mod, not in
// go.sum and not in vendor/, so this is the one task that reaches the network
// for a tool. What it downloads is asked of the checksum database, and the two
// settings that turn that verification off are cleared here, so a proxy or a
// mirror cannot answer with a module that is not the one the version names.
// GOFLAGS carries the module mode, since the vendor directory of this module
// cannot resolve a tool that is not in it.
func winresEnv() []string {
	return []string{
		"GOFLAGS=-mod=mod",
		"GOSUMDB=sum.golang.org",
		"GONOSUMDB=",
		"GOPRIVATE=",
	}
}

func taskResources(root string) error {
	// It writes one rsrc_windows_<arch>.syso per architecture, and the version
	// goes on the command line so that winres.json holds no number to go stale.
	// The generated files are committed and no release step runs this task, so a
	// release is built from what the repository holds; run it only when the
	// resources are meant to change.
	version, err := appVersion(root)
	if err != nil {
		return err
	}
	return runGo(root, winresEnv(), "run", winresVersion, "make",
		"--in", resConfig, "--out", resPrefix, "--arch", "amd64,arm64,386",
		"--file-version", version, "--product-version", version)
}

// taskVersion prints the version of the tree and the spelling each format is
// named with, one per line as name=value. A caller that has to know the name of
// a package asks here rather than working the version out a second time: the
// spellings differ per format, and that rule copied into a workflow is a copy
// that goes stale the next time one of them changes.
func taskVersion(root string) error {
	report, err := versionReport(root)
	if err != nil {
		return err
	}
	fmt.Print(report)
	return nil
}

// versionReport is the text of that task. The workflow reads the deb= line out
// of it to find the package it expects, so the shape is a contract and a test
// holds it.
func versionReport(root string) (string, error) {
	rel, err := packageVersion(root)
	if err != nil {
		return "", err
	}
	version, release := rel.rpm()
	return fmt.Sprintf("tag=%s\ndeb=%s\npacman=%s\nrpm=%s-%s\n",
		rel.tag, rel.deb(), rel.pacman(), version, release), nil
}

func taskTest(root string) error {
	if err := runGo(root, nil, "vet", "./..."); err != nil {
		return err
	}
	// The Windows sources are never compiled by the job this runs in, so a type
	// error or a test that does not compile there would only be found on a
	// release build. CGO is off because there is no Windows C toolchain here,
	// and one is not needed: the whole program is pure Go once the Windows
	// files are selected. macOS is left out on purpose: the vendored systray
	// backend there is cgo only, so it cannot be type checked from Linux.
	//
	// What this does NOT do is run anything on Windows, and the difference has
	// already cost this project a real defect: a directory was checked for
	// privacy by reading its mode bits, which every NTFS directory reports as
	// 0777, so the check passed a directory every account could read. It type
	// checked, it linked, and only running it on Windows showed the fault.
	// vet compiles the Windows test files, so a Windows runner would add
	// execution and nothing else; the job that runs this has no such runner.
	// To close the gap by hand, from a Linux checkout:
	//   CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o x.exe ./internal/config/
	// then run x.exe on a Windows machine in a directory it can write to
	// (Go refuses to start an executable found by a relative name, so invoke it
	// as .\x.exe from the directory that holds it).
	if err := runGo(root, []string{"CGO_ENABLED=0", "GOOS=windows"}, "vet", "./..."); err != nil {
		return err
	}
	// Under the race detector, which needs cgo and a C compiler. A field the
	// check loop reads while a restart writes it passes a plain run and shows up
	// only here, and the settings page reaches that path from two tabs at once.
	return runGo(root, nil, "test", "-race", "./...")
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

// taskClean removes the built executables. The settings and the log live next to
// the executable, so they are left alone: cleaning a build must not throw away an
// account.
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

	// The packages are made from those executables, so they go with them: one left
	// behind would describe the build before this one.
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
// the size of the file.
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
