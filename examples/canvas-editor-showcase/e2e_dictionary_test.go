//go:build !(js && wasm)

package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/spell/dictserver"
)

// TestSpellingLoadsAnotherLanguagesDictionaryOnDemand serves a German dictionary (with a compound flag) from a
// dictionary server, chooses it in the Spelling tab and checks a compound word that English would flag.
func TestSpellingLoadsAnotherLanguagesDictionaryOnDemand(t *testing.T) {
	fsys := fstest.MapFS{
		"de_DE/de_DE.aff": {Data: []byte("SET UTF-8\nTRY enisrtalhdu\nCOMPOUNDFLAG Z\nCOMPOUNDMIN 3\n")},
		"de_DE/de_DE.dic": {Data: []byte("3\nhaus/Z\ntür/Z\nschloss\n")},
		"names.json":      {Data: []byte(`{"de_DE":"Deutsch"}`)},
	}
	h := http.StripPrefix("/dict", &dictserver.Server{FS: fsys})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		h.ServeHTTP(w, r)
	}))
	defer ts.Close()

	d := e2etest.NewSite(t).Open(t, "?lang=en&dictapi="+ts.URL+"/dict")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Type(" haustür xyzzq ")
	d.Click(`button[data-tab="spell"]`)
	d.Click(".gj-spell-enable")
	d.WaitSelector(".gj-spell-dict")
	d.OK(slices.Contains(d.Texts(".gj-spell-word"), "haustür"), "English flags the German compound: "+strings.Join(d.Texts(".gj-spell-word"), " "))
	d.Select(".gj-spell-dict", "de_DE")
	d.WaitFor("the German dictionary", `(document.querySelector('.gj-spell-info') || {textContent: ''}).textContent.includes('Deutsch') && !document.querySelector('.gj-spell-dictnote')`)
	words := d.Texts(".gj-spell-word")
	d.OK(!slices.Contains(words, "haustür") && slices.Contains(words, "xyzzq"), "the compound is a word now and the nonsense is not: "+strings.Join(words, " "))
	d.Select(".gj-spell-dict", "en")
	d.WaitFor("English again", `(document.querySelector('.gj-spell-info') || {textContent: ''}).textContent.includes('English')`)
	d.NoErrors()
}
