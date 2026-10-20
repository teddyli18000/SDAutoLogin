package main

import (
	"fmt"
	"regexp"
	"strings"
)

// releaseVersion is what a tag names, split into the parts the packaging formats
// need. The formats disagree about how a version that comes before the release
// is spelled, and the disagreement is not cosmetic: Debian compares a plain
// "1.3.0-alpha.1" as later than "1.3.0", so a machine that installed the
// pre-release would never be offered the release it leads to. Each format is
// asked for its own spelling below.
//
// A tag with nothing after the number is an ordinary release, and every spelling
// is then the number on its own, which is what this program published before
// there were pre-releases.
type releaseVersion struct {
	// tag is what the tag named. The program reports it, the Windows archive is
	// named after it, and a manifest points at the release of that tag.
	tag string
	// number is the version without the label, and label is what comes after the
	// hyphen, empty for a release.
	number string
	label  string
}

var (
	reVersionNumber = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)*$`)
	reVersionLabel  = regexp.MustCompile(`^[0-9A-Za-z]+(\.[0-9A-Za-z]+)*$`)
)

// parseRelease splits the version a tag names. One that is neither a number nor
// a number with a label after a hyphen is refused here rather than written into
// five formats: each format rejects different characters, so a version that
// reaches them unchecked fails in the middle of a release.
func parseRelease(tag string) (releaseVersion, error) {
	number, label, labelled := strings.Cut(tag, "-")
	v := releaseVersion{tag: tag, number: number, label: label}
	if !reVersionNumber.MatchString(number) {
		return v, fmt.Errorf("%q does not start with a version number", tag)
	}
	if labelled && !reVersionLabel.MatchString(label) {
		return v, fmt.Errorf("%q has a label that cannot go into a package", tag)
	}
	return v, nil
}

// packageVersion is the version the packages are named and described with.
func packageVersion(root string) (releaseVersion, error) {
	tag, err := appVersion(root)
	if err != nil {
		return releaseVersion{}, err
	}
	return parseRelease(tag)
}

// deb is the spelling Debian and OpenWrt compare. The tilde is what puts a
// pre-release before the release it leads to: "1.3.0~alpha.1" sorts before
// "1.3.0", and "1.3.0-alpha.1" sorts after it.
func (v releaseVersion) deb() string {
	if v.label == "" {
		return v.number
	}
	return v.number + "~" + v.label
}

// pacman is the Arch spelling. vercmp sorts a label written onto the number
// before the number itself and one after a dot after it, so the label is joined
// on without a separator.
func (v releaseVersion) pacman() string {
	if v.label == "" {
		return v.number
	}
	return v.number + v.label
}

// rpm is RPM's pair. Its version field carries no label, and the release says
// which pre-release it is: "0.1.alpha.1" sorts before the release "1", so a
// machine that installed the pre-release is offered the release.
func (v releaseVersion) rpm() (version, release string) {
	if v.label == "" {
		return v.number, pkgRelease
	}
	return v.number, "0.1." + v.label
}
