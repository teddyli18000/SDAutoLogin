package main

import (
	"strings"
	"testing"
)

// A release has to come out of every spelling exactly as it did before there
// were pre-releases, or publishing one would rename every package.
func TestAReleaseIsSpelledTheSameEverywhere(t *testing.T) {
	rel, err := parseRelease("1.2.0")
	if err != nil {
		t.Fatalf("parseRelease: %v", err)
	}
	if got := rel.deb(); got != "1.2.0" {
		t.Errorf("deb = %q, want %q", got, "1.2.0")
	}
	if got := rel.pacman(); got != "1.2.0" {
		t.Errorf("pacman = %q, want %q", got, "1.2.0")
	}
	version, release := rel.rpm()
	if version != "1.2.0" || release != pkgRelease {
		t.Errorf("rpm = %q-%q, want %q-%q", version, release, "1.2.0", pkgRelease)
	}
}

// The spellings differ because the formats sort a version before the release
// differently, and getting it wrong is what strands a machine on a pre-release.
// Debian's comparison, run against the strings below:
//
//	dpkg --compare-versions 1.3.0~alpha.1-1 lt 1.3.0-1   true
//	dpkg --compare-versions 1.3.0-alpha.1-1 lt 1.3.0-1   false
func TestAPreReleaseSortsBeforeTheReleaseItLeadsTo(t *testing.T) {
	rel, err := parseRelease("1.3.0-alpha.1")
	if err != nil {
		t.Fatalf("parseRelease: %v", err)
	}
	if got := rel.deb(); got != "1.3.0~alpha.1" {
		t.Errorf("deb = %q, want %q", got, "1.3.0~alpha.1")
	}
	// vercmp sorts a label written onto the number before the number itself, so
	// "1.3.0alpha.1" is before "1.3.0" and "1.3.0.alpha.1" is after it.
	if got := rel.pacman(); got != "1.3.0alpha.1" {
		t.Errorf("pacman = %q, want %q", got, "1.3.0alpha.1")
	}
	// RPM carries no label in its version, and a release below "1" sorts before
	// the release of the final.
	version, release := rel.rpm()
	if version != "1.3.0" {
		t.Errorf("rpm version = %q, want %q", version, "1.3.0")
	}
	if release != "0.1.alpha.1" {
		t.Errorf("rpm release = %q, want %q", release, "0.1.alpha.1")
	}
	// The tag is what the program reports and what a manifest points at, so it
	// is kept whole.
	if rel.tag != "1.3.0-alpha.1" {
		t.Errorf("tag = %q, want it kept whole", rel.tag)
	}
}

// A tag the formats cannot carry is refused where the version is read, before
// five packages are built around it. The formats reject different characters,
// so the rule here is the narrow one they all accept.
func TestAVersionNoFormatCanCarryIsRefused(t *testing.T) {
	for _, tag := range []string{
		"",
		"v1.3.0",
		"1.3",
		"1.3.0-",
		"1.3.0-alpha_1",
		"1.3.0-alpha 1",
		"1.3.0-alpha/1",
		"1.3.0-alpha+build",
	} {
		if _, err := parseRelease(tag); err == nil {
			t.Errorf("parseRelease(%q) was accepted", tag)
		}
	}
}

// The labels a pre-release is likely to carry, which are the ones that must
// work.
func TestTheLabelsAPreReleaseCarries(t *testing.T) {
	for _, c := range []struct{ tag, deb, pacman string }{
		{"2.0.0-rc.1", "2.0.0~rc.1", "2.0.0rc.1"},
		{"1.3.0-beta.2", "1.3.0~beta.2", "1.3.0beta.2"},
		{"1.3.0-alpha", "1.3.0~alpha", "1.3.0alpha"},
	} {
		rel, err := parseRelease(c.tag)
		if err != nil {
			t.Errorf("parseRelease(%q): %v", c.tag, err)
			continue
		}
		if got := rel.deb(); got != c.deb {
			t.Errorf("%s: deb = %q, want %q", c.tag, got, c.deb)
		}
		if got := rel.pacman(); got != c.pacman {
			t.Errorf("%s: pacman = %q, want %q", c.tag, got, c.pacman)
		}
	}
}

// The workflow reads the deb= line of this out of the task to find the package
// it expects, so the shape is a contract rather than a convenience.
func TestTheVersionReportCarriesTheNameTheWorkflowLooksFor(t *testing.T) {
	root := testRoot(t)
	report, err := versionReport(root)
	if err != nil {
		t.Fatalf("versionReport: %v", err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(report), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("the line %q is not name=value", line)
		}
		want[name] = value
	}
	for _, key := range []string{"tag", "deb", "pacman", "rpm"} {
		if want[key] == "" {
			t.Errorf("the report has no %s= line:\n%s", key, report)
		}
	}
	// The deb is the one the workflow checks for, and it is not the tag.
	tag, err := appVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	if want["tag"] != tag {
		t.Errorf("tag = %q, want %q", want["tag"], tag)
	}
	if want["deb"] != parseOrFail(t, tag).deb() {
		t.Errorf("deb = %q, want the spelling the deb is named with", want["deb"])
	}
}

func parseOrFail(t *testing.T, tag string) releaseVersion {
	t.Helper()
	rel, err := parseRelease(tag)
	if err != nil {
		t.Fatalf("parseRelease(%q): %v", tag, err)
	}
	return rel
}
