package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"
)

// testRoot is the repository the test runs in.
func testRoot(t *testing.T) string {
	t.Helper()

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("finding the repository: %v", err)
	}
	return root
}

// TestTheSourcesCarryTheVersionOfTheLatestTag holds the fallback to the tag. The
// version a release is named after comes from the tag, and what the sources carry
// is what a tree without a checkout falls back on: letting the two drift would
// mean a build from a tarball reports a release it was not cut from.
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
// names the git ref to fetch, so the number has to be written in its Makefile,
// and nothing but a check keeps it in step.
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
// declares its strings in: a constant of its own and a block of variables. The value
// is read with a pattern over the source, so a shape it does not expect reads as a
// value that is not there, and the packaging stops over a declaration that is fine.
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
// the tag: a tree that was unpacked rather than checked out has none, and still
// has to build into a program that can name its version.
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

// TestTheBuildFlagsCarryTheVersionAndTheCommit covers what the release is written
// into the program with. The two are read from the tree being built, so a release
// names itself, and a build that is in front of someone can be named exactly.
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

// TestTheLinkerWritesIntoTheVariablesTheProgramReads covers the way -X fails: it
// names a variable by its full path, and a path that is wrong or no longer exists
// is not an error. The linker then writes nothing, the build succeeds, and the
// program quietly reports the fallback instead of the release it was cut from.
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

// TestTheWindowsResourcesCarryTheVersionOfTheSources covers the one generated file a
// build links rather than makes. The resources task writes it for the tag the tree is
// at and nothing regenerates it in the meantime, so a tag that moved without that task
// being run leaves the version Explorer shows naming the release before it, while the
// program reports the new one.
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

// modulePath is the module named in go.mod, which is the prefix of every package
// path a build setting can name.
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
