// Package update asks the forge whether a newer release exists, checks that
// what it downloads is the release it says it is, and installs it when the copy
// that is running is in a position to replace itself.
package update

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a tag split the way the comparison reads it: the numbers, and the
// label that puts it before the release it leads to.
type Version struct {
	tag     string
	numbers []int
	label   []string
}

var (
	reNumbers = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
	reLabel   = regexp.MustCompile(`^[0-9A-Za-z]+(\.[0-9A-Za-z]+)*$`)
)

// ParseVersion reads a tag such as "v1.3.0" or "v1.3.0-alpha.1". The leading v
// is optional, since a tag carries it and app.Version does not.
func ParseVersion(tag string) (Version, error) {
	v := Version{tag: tag}
	rest := strings.TrimPrefix(tag, "v")

	// Build metadata is not part of the order and is dropped, as semver says.
	if i := strings.IndexByte(rest, '+'); i >= 0 {
		rest = rest[:i]
	}
	numbers, label, labelled := strings.Cut(rest, "-")
	if !reNumbers.MatchString(numbers) {
		return v, fmt.Errorf("%q does not name a version", tag)
	}
	for _, part := range strings.Split(numbers, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return v, fmt.Errorf("%q does not name a version", tag)
		}
		v.numbers = append(v.numbers, n)
	}
	if labelled {
		if !reLabel.MatchString(label) {
			return v, fmt.Errorf("%q has a label this program cannot compare", tag)
		}
		v.label = strings.Split(label, ".")
	}
	return v, nil
}

// Tag is the version as it was written.
func (v Version) Tag() string { return v.tag }

// IsPrerelease is whether the tag carries a label, which is what the forge
// marks a release as and what an update channel selects on.
func (v Version) IsPrerelease() bool { return len(v.label) > 0 }

// Compare orders two versions: negative when v comes first, zero when they are
// the same, positive when v comes later.
//
// The rule that matters here is the third one: a label puts a version *before*
// the release it leads to, so 1.3.0-alpha.1 is older than 1.3.0. Reading the
// label as a suffix that makes a version bigger would leave a machine on a
// pre-release for good, since the release it leads to would look older than
// what is installed.
func (v Version) Compare(o Version) int {
	for i := 0; i < len(v.numbers) || i < len(o.numbers); i++ {
		a, b := 0, 0
		if i < len(v.numbers) {
			a = v.numbers[i]
		}
		if i < len(o.numbers) {
			b = o.numbers[i]
		}
		if a != b {
			if a < b {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(v.label) == 0 && len(o.label) == 0:
		return 0
	case len(v.label) == 0:
		return 1
	case len(o.label) == 0:
		return -1
	}
	for i := 0; i < len(v.label) || i < len(o.label); i++ {
		if i >= len(v.label) {
			return -1
		}
		if i >= len(o.label) {
			return 1
		}
		a, b := v.label[i], o.label[i]
		an, aerr := strconv.Atoi(a)
		bn, berr := strconv.Atoi(b)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aerr == nil:
			// A number comes before a word, as semver orders them.
			return -1
		case berr == nil:
			return 1
		default:
			if a != b {
				if a < b {
					return -1
				}
				return 1
			}
		}
	}
	return 0
}

// NewerThan is whether v comes after o.
func (v Version) NewerThan(o Version) bool { return v.Compare(o) > 0 }
