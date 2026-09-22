package webui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"sdautologin/internal/i18n"
)

// markupText finds every element of the page whose text the script replaces
// from the string table, together with the literal written in the markup.
var markupText = regexp.MustCompile(`data-i18n="([^"]+)"[^>]*>([^<]*)<`)

// TestThePageQuotesTheStringTable keeps the literals in index.html from drifting
// away from the table they are replaced by. Those literals are only visible
// before the script runs, or if it never runs, so a stale one is easy to leave
// behind.
func TestThePageQuotesTheStringTable(t *testing.T) {
	page, err := fs.ReadFile(Assets(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	table := i18n.ALL(i18n.LangEN)

	checked := 0
	for _, m := range markupText.FindAllStringSubmatch(string(page), -1) {
		key, literal := m[1], strings.TrimSpace(m[2])
		// An empty element is filled in by the script alone, so it holds
		// nothing that could drift.
		if literal == "" {
			continue
		}
		want, ok := table[key]
		if !ok {
			t.Errorf("index.html names the key %q, which the string table does not hold", key)
			continue
		}
		checked++
		if literal != want {
			t.Errorf("index.html writes %q for %q where the table says %q", literal, key, want)
		}
	}
	if checked == 0 {
		t.Error("not one literal in the page was compared against the table")
	}
}
