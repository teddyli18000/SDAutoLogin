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
// which may take arguments after it, and state.i18n["key"]. A key the script puts
// together while it runs, like the ret_code and state ones, matches neither,
// since the quoted part has to be the whole key. The word boundary in front of
// t( keeps the calls that merely end in one - closest(".account"), split("; ") -
// from being read as calls to it.
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
