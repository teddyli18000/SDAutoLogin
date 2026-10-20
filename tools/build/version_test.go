package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

// testRoot is the repository the test runs in. These tests read the tree the
// version is written in - go.mod, the packaging files, the Windows resource file
// - so the tree has to be around the test binary, which is how `go test` starts
// one. A cross compiled Windows binary runs from wherever it was copied, and
// there is no repository above C:\, so it has nothing to read and no version to
// check there, while the property these tests are about, that the sources and
// the name a release carries do not drift apart, belongs to the tree itself and
// is checked where the tree is, by the toolchain that compiles it in.
func testRoot(t *testing.T) string {
	t.Helper()

	root, err := repoRoot()
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("this binary runs outside a repository (%v), so the tree these tests read is not here; the version they check comes from the same sources on every platform", err)
		}
		t.Fatalf("finding the repository: %v", err)
	}
	return root
}

// TestTheSourcesCarryTheVersionOfTheLatestTag holds the fallback to the tag.
// The version a release is named after comes from the tag, and the sources
// carry what a tree without a checkout falls back on: letting the two drift
// would misname a build from a tarball.
func TestTheSourcesCarryTheVersionOfTheLatestTag(t *testing.T) {
	root := testRoot(t)

	tag, ok := gitTag(root)
	if !ok {
		t.Skip("this tree has no tag, so there is no release for the sources to agree with")
	}
	source, err := appSourceString(root, "Version")
	if err != nil {
		t.Fatal(err)
	}
	if source != tag {
		t.Errorf("the sources carry version %q, the latest tag names %q", source, tag)
	}
}

// TestTheOpenWrtPackageNamesTheSameVersion covers the one copy of the version
// that cannot be read out of the sources when it is needed: the OpenWrt package
// names the git ref to fetch, so the number is written in its Makefile.
func TestTheOpenWrtPackageNamesTheSameVersion(t *testing.T) {
	root := testRoot(t)

	source, err := appSourceString(root, "Version")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "packaging", "openwrt", "Makefile")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^PKG_VERSION:=(.+)$`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("no PKG_VERSION in %s", path)
	}
	if got := strings.TrimSpace(string(m[1])); got != source {
		t.Errorf("%s names version %q, the sources carry %q", path, got, source)
	}
}

// TestAValueIsReadFromEitherDeclaration covers the two shapes the payload file
// declares its strings in: a constant of its own and a block of variables. The
// value is read with a pattern over the source, so a shape it does not expect
// reads as a missing value.
func TestAValueIsReadFromEitherDeclaration(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package app

const Repo = "https://example.invalid/one"

var (
	Version = "1.2.3"
	Commit  = ""
)
`
	if err := os.WriteFile(filepath.Join(dir, "payload.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ name, want string }{
		{"Repo", "https://example.invalid/one"},
		{"Version", "1.2.3"},
		{"Commit", ""},
	} {
		got, err := appSourceString(root, c.name)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestAVersionComesFromTheSourcesWhenThereIsNoTag is the other half of reading
// the tag: a tree with no checkout has none, and still has to build into a
// program that can name its version.
func TestAVersionComesFromTheSourcesWhenThereIsNoTag(t *testing.T) {
	root := testRoot(t)
	want, err := appSourceString(root, "Version")
	if err != nil {
		t.Fatal(err)
	}

	// The payload file on its own, in a directory that is not a checkout.
	unpacked := t.TempDir()
	dir := filepath.Join(unpacked, "internal", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(root, "internal", "app", "payload.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := appVersion(unpacked)
	if err != nil {
		t.Fatalf("appVersion in a tree with no checkout: %v", err)
	}
	if got != want {
		t.Errorf("appVersion with no tag = %q, want the version in the sources (%q)", got, want)
	}
}

// TestTheBuildFlagsCarryTheVersionAndTheCommit covers what the release is
// written into the program with. Both are read from the tree being built, so a
// release names itself, and a build in front of someone can be named.
func TestTheBuildFlagsCarryTheVersionAndTheCommit(t *testing.T) {
	root := testRoot(t)

	flags, err := ldflagsFor(root, true, "windows")
	if err != nil {
		t.Fatal(err)
	}
	version, err := appVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(flags, versionVar+"="+version) {
		t.Errorf("the flags carry no version: %s", flags)
	}
	if commit := appCommit(root); commit != "" && !strings.Contains(flags, commitVar+"="+commit) {
		t.Errorf("the flags carry no commit: %s", flags)
	}
	if !strings.Contains(flags, "-H=windowsgui") {
		t.Errorf("the flags lost the windowless link of the Windows build: %s", flags)
	}
	if plain, err := ldflagsFor(root, false, "windows"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(plain, "-H=windowsgui") {
		t.Errorf("the console build is linked as a windowless one: %s", plain)
	}
}

// TestTheLinkerWritesIntoTheVariablesTheProgramReads covers the way -X fails:
// it names a variable by its full path, and a path that is wrong or missing is
// not an error. The linker writes nothing, the build succeeds, and the program
// reports the fallback.
func TestTheLinkerWritesIntoTheVariablesTheProgramReads(t *testing.T) {
	root := testRoot(t)
	module := modulePath(t, root)

	for _, c := range []struct{ setting, name string }{
		{versionVar, "Version"},
		{commitVar, "Commit"},
	} {
		if want := module + "/internal/app." + c.name; c.setting != want {
			t.Errorf("the linker is told to write %s, want %s", c.setting, want)
		}
		if _, err := appSourceString(root, c.name); err != nil {
			t.Errorf("the linker writes %s, which the sources do not declare: %v", c.name, err)
		}
	}
}

// TestTheLinkerWritesTheUpdaterItsVersion covers the second variable the version
// goes into. The updater holds a release against the version of the running
// program, and it cannot read the variable above: internal/app is the package
// that imports it, so the linker writes the same version into internal/update.
//
// A -X that names nothing is not an error — the build succeeds and the variable
// stays empty — and an empty one is a program that refuses to install anything.
// That is the safe direction, and this is what keeps it from happening quietly.
func TestTheLinkerWritesTheUpdaterItsVersion(t *testing.T) {
	root := testRoot(t)
	module := modulePath(t, root)

	if want := module + "/internal/update.BuildVersion"; updaterVersionVar != want {
		t.Errorf("the linker is told to write %s, want %s", updaterVersionVar, want)
	}
	flags, err := ldflagsFor(root, true, "windows")
	if err != nil {
		t.Fatal(err)
	}
	version, err := appVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(flags, updaterVersionVar+"="+version) {
		t.Errorf("the flags carry no version for the updater: %s", flags)
	}
	if !declaresStringVar(t, root, "internal/update", "BuildVersion") {
		t.Errorf("the linker writes %s, which the sources do not declare", updaterVersionVar)
	}
}

// TestTheResourceGeneratorVerifiesWhatItDownloads covers the one task that runs
// a tool from outside this module: it is pinned by version, and the checksum
// database is named while the settings that would turn that verification off are
// cleared, so a proxy cannot answer with something else under the pinned name.
func TestTheResourceGeneratorVerifiesWhatItDownloads(t *testing.T) {
	env := winresEnv()
	for _, want := range []string{
		"GOFLAGS=-mod=mod", "GOSUMDB=sum.golang.org", "GONOSUMDB=", "GOPRIVATE=",
	} {
		found := false
		for _, got := range env {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the resource generator runs without %s: %v", want, env)
		}
	}
}

// declaresStringVar is whether a package of this tree declares a string variable
// under that name. -X names a variable by its full path and writes nothing when
// the path or the name is wrong, so the declaration is what the linker's
// argument is checked against.
func declaresStringVar(t *testing.T, root, pkg, name string) bool {
	t.Helper()

	dir := filepath.Join(root, filepath.FromSlash(pkg))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "var " + name + " string"
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), want) {
			return true
		}
	}
	return false
}

// TestTheWindowsResourcesCarryTheVersionOfTheSources covers the one generated
// file a build links. The resources task writes it for the tag the tree is at;
// when the tag moves without a run of that task, Explorer shows the release
// before the one the program reports.
func TestTheWindowsResourcesCarryTheVersionOfTheSources(t *testing.T) {
	root := testRoot(t)
	version, err := appSourceString(root, "Version")
	if err != nil {
		t.Fatal(err)
	}
	// The version is stored the way Windows stores strings in a version resource.
	want := utf16Bytes(version)

	for _, arch := range []string{"amd64", "arm64", "386"} {
		path := filepath.Join(root, "winres", "rsrc_windows_"+arch+".syso")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v (run: go run ./tools/build resources)", describe(root, path), err)
			continue
		}
		if !bytes.Contains(data, want) {
			t.Errorf("%s does not carry the version the sources name (%q); run: go run ./tools/build resources",
				describe(root, path), version)
		}
	}
}

// utf16Bytes is s as the little endian UTF-16 a Windows version resource holds its
// strings in.
func utf16Bytes(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2*len(units))
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

// modulePath is the module named in go.mod, the prefix of every package path
// the linker can be told to write into.
func modulePath(t *testing.T, root string) string {
	t.Helper()

	src, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no module line in go.mod")
	}
	return string(m[1])
}
