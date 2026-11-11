//go:build !(js && wasm)

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/internal/screenreader"
)

func phrases(sr *screenreader.Reader) []string {
	var out []string
	for _, it := range sr.Items() {
		out = append(out, it.Phrase)
	}
	return out
}

func hasPhrase(list []string, want string) bool { return slices.Contains(list, want) }

func hasPhraseWith(list []string, part string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.Contains(s, part) })
}

func TestMirrorStructureForAScreenReader(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	sr := screenreader.New(t, d.Context())
	ph := phrases(sr)
	for _, want := range []string{
		"Document outline, navigation landmark",
		"Gojiberry editor, link",
		"Second thoughts, link",
		"Gojiberry editor, heading, level 1",
		"Second thoughts, heading, level 2",
		"Gojiberry on GitHub, link",
		"Feature, column header",
		"Keys, column header",
	} {
		if !hasPhrase(ph, want) {
			t.Errorf("a screen reader should say %q; it reads:\n%s", want, strings.Join(ph, "\n"))
		}
	}
	if slices.Index(ph, "Document outline, navigation landmark") > slices.Index(ph, "Gojiberry editor, heading, level 1") {
		t.Error("the outline comes before the text")
	}
	d.OK(d.Attr("#a11y-text a[href^='https://github.com']", "rel") == "noopener noreferrer", "the document's link opens safely")

	want := strings.Index(d.State().Str("text"), "Second thoughts")
	d.OK(want > 0, "the heading is in the text")
	d.Eval(`[...document.querySelectorAll('#a11y-text-outline a')].find(a => a.textContent === 'Second thoughts').click()`, nil)
	d.Wait("s.cursors[0].Caret === " + e2etest.J(len([]rune(d.State().Str("text")[:want]))))
	d.OK(d.Str(`document.activeElement.textContent`) == "Second thoughts", "and the heading has the keyboard focus, so it is read: "+d.Str(`document.activeElement.textContent`))
	d.NoErrors()
}
