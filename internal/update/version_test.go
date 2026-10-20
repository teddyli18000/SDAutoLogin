package update

import (
	"sort"
	"testing"
)

func mustParse(t *testing.T, tag string) Version {
	t.Helper()
	v, err := ParseVersion(tag)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", tag, err)
	}
	return v
}

// A label puts a version before the release it leads to. This is the rule the
// whole feature rests on: read the other way round, a machine that installed
// the pre-release would never be offered the release, because the release would
// look older than what is already there.
func TestAPreReleaseComesBeforeTheReleaseItLeadsTo(t *testing.T) {
	alpha := mustParse(t, "v1.3.0-alpha.1")
	final := mustParse(t, "v1.3.0")
	if alpha.Compare(final) >= 0 {
		t.Errorf("%s compares after %s", alpha.Tag(), final.Tag())
	}
	if !final.NewerThan(alpha) {
		t.Errorf("%s is not newer than %s", final.Tag(), alpha.Tag())
	}
	// And a machine on the release before it is offered the pre-release, which
	// is what the channel that allows them is for.
	if !alpha.NewerThan(mustParse(t, "v1.2.0")) {
		t.Errorf("%s is not newer than v1.2.0", alpha.Tag())
	}
}

func TestTheOrderOfTwoVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v1.2.0", "v1.2.0", 0},
		{"1.2.0", "v1.2.0", 0},
		{"v1.2.0", "v1.3.0", -1},
		{"v1.3.0", "v1.2.9", 1},
		{"v1.10.0", "v1.9.0", 1},
		{"v2.0.0", "v1.99.99", 1},
		// A shorter version is padded, so these are the same.
		{"v1.2", "v1.2.0", 0},
		{"v1", "v1.0.0", 0},
		// Labels, in the order semver gives them.
		{"v1.0.0-alpha", "v1.0.0-alpha.1", -1},
		{"v1.0.0-alpha.1", "v1.0.0-alpha.2", -1},
		{"v1.0.0-alpha.2", "v1.0.0-alpha.10", -1},
		{"v1.0.0-alpha.10", "v1.0.0-beta", -1},
		{"v1.0.0-beta", "v1.0.0-beta.2", -1},
		{"v1.0.0-beta.2", "v1.0.0-rc.1", -1},
		{"v1.0.0-rc.1", "v1.0.0", -1},
		// A number comes before a word.
		{"v1.0.0-alpha.1", "v1.0.0-alpha.beta", -1},
		// The numbers are read before the label, whatever the labels say.
		{"v1.0.1-alpha", "v1.0.0", 1},
		{"v1.0.0-alpha", "v0.9.9", 1},
		// Build metadata does not order anything.
		{"v1.0.0+2", "v1.0.0+1", 0},
	} {
		a, b := mustParse(t, c.a), mustParse(t, c.b)
		if got := a.Compare(b); got != c.want {
			t.Errorf("%s.Compare(%s) = %d, want %d", c.a, c.b, got, c.want)
		}
		// The other direction has to say the opposite, or sorting is not a
		// total order and the release picked out of a list depends on the order
		// the forge happened to answer in.
		if got := b.Compare(a); got != -c.want {
			t.Errorf("%s.Compare(%s) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}

// Sorting a list the forge answered in some other order has to put the newest
// last, whichever way the list came in.
func TestSortingPutsTheNewestLast(t *testing.T) {
	tags := []string{
		"v1.0.0", "v1.3.0-alpha.1", "v1.2.0", "v1.3.0-rc.1",
		"v1.1.0", "v1.3.0-beta.2", "v1.3.0", "v0.9.0",
	}
	want := []string{
		"v0.9.0", "v1.0.0", "v1.1.0", "v1.2.0",
		"v1.3.0-alpha.1", "v1.3.0-beta.2", "v1.3.0-rc.1", "v1.3.0",
	}
	vs := make([]Version, 0, len(tags))
	for _, tag := range tags {
		vs = append(vs, mustParse(t, tag))
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Compare(vs[j]) < 0 })
	for i, v := range vs {
		if v.Tag() != want[i] {
			t.Fatalf("sorted[%d] = %s, want %s\n got: %v", i, v.Tag(), want[i], vs)
		}
	}
}

func TestALabelIsWhatMakesAPreRelease(t *testing.T) {
	for tag, want := range map[string]bool{
		"v1.2.0":         false,
		"v1.3.0-alpha.1": true,
		"v1.3.0-rc.1":    true,
		"v2.0.0+3":       false,
	} {
		if got := mustParse(t, tag).IsPrerelease(); got != want {
			t.Errorf("%s.IsPrerelease() = %v, want %v", tag, got, want)
		}
	}
}

// A tag the forge carries that this program cannot read is refused where it is
// parsed, so it is passed over rather than compared on a guess.
func TestAVersionThatCannotBeReadIsRefused(t *testing.T) {
	for _, tag := range []string{
		"", "v", "nightly", "v1.x.0", "v1.2.0-", "v1.2.0-alpha_1",
		"v1.2.0-alpha 1", "release-1.2.0",
	} {
		if _, err := ParseVersion(tag); err == nil {
			t.Errorf("ParseVersion(%q) was accepted", tag)
		}
	}
}
