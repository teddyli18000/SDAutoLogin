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
// away from the table they are replaced by. They are only visible before the
// script runs, or if it never runs, so a stale one is easy to leave behind.
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

// scriptText holds the two shapes in which the script names a string: t("key"),
// which may take arguments after it, and state.i18n["key"]. The word boundary
// before t( keeps calls that merely end in one from being read as calls to it.
var scriptText = []*regexp.Regexp{
	regexp.MustCompile(`\bt\(["']([^"']+)["'][),]`),
	regexp.MustCompile(`state\.i18n\[["']([^"']+)["']\]`),
}

// TestTheScriptQuotesTheStringTable is the other half of
// TestThePageQuotesTheStringTable: the script names most of the page's strings
// itself, and a renamed or mistyped key shows on screen as the key, not the wording.
func TestTheScriptQuotesTheStringTable(t *testing.T) {
	script, err := fs.ReadFile(Assets(), "app.js")
	if err != nil {
		t.Fatal(err)
	}
	table := i18n.ALL(i18n.LangEN)

	checked := 0
	for _, names := range scriptText {
		for _, m := range names.FindAllStringSubmatch(string(script), -1) {
			key := m[1]
			checked++
			if _, ok := table[key]; !ok {
				t.Errorf("app.js names the key %q, which the string table does not hold", key)
			}
		}
	}
	if checked == 0 {
		t.Error("not one key in the script was compared against the table")
	}
}

// bootFailedWording holds the two wordings bootFailedText falls back on, in the
// order the function returns them: Chinese first, English second.
var bootFailedWording = regexp.MustCompile(`(?s)function bootFailedText\(\).*?return "([^"]+)".*?return "([^"]+)"`)

// TestTheBootFailureQuotesTheStringTable holds that fallback to the table. It is
// the one message the script cannot read from the table, because the bootstrap
// that carries the table is what failed, so nothing else would notice it going
// out of step with the msg.stale the toasts show.
func TestTheBootFailureQuotesTheStringTable(t *testing.T) {
	script, err := fs.ReadFile(Assets(), "app.js")
	if err != nil {
		t.Fatal(err)
	}
	m := bootFailedWording.FindStringSubmatch(string(script))
	if m == nil {
		t.Fatal("app.js has no bootFailedText returning two wordings")
	}
	for _, c := range []struct {
		lang i18n.Lang
		got  string
	}{{i18n.LangZH, m[1]}, {i18n.LangEN, m[2]}} {
		want := i18n.ALL(c.lang)["msg.stale"]
		if want == "" {
			t.Fatalf("the string table has no msg.stale in %s", c.lang)
		}
		if c.got != want {
			t.Errorf("app.js writes %q in %s where the table says %q", c.got, c.lang, want)
		}
	}
}

// The script carries a UTF-8 byte order mark, for the editors that would
// otherwise read the Chinese strings in it as the local code page. Two edits
// have dropped it already. This test is what stops a third.
func TestTheScriptKeepsItsByteOrderMark(t *testing.T) {
	script, err := fs.ReadFile(Assets(), "app.js")
	if err != nil {
		t.Fatal(err)
	}
	bom := []byte{0xEF, 0xBB, 0xBF}
	if len(script) < len(bom) || string(script[:len(bom)]) != string(bom) {
		t.Errorf("app.js does not start with a byte order mark: % x", script[:min(len(script), 8)])
	}
}
