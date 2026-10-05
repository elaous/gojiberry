//go:build !(js && wasm)

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// cors lets the page (another origin) call a fake backend.
func cors(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			return
		}
		h(w, r)
	})
}

// TestLanguageTab drives the Language tab against fake translate and grammar
// servers.
func TestLanguageTab(t *testing.T) {
	tr := httptest.NewServer(cors(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Text, TargetLang string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]string{"translatedText": "[" + req.TargetLang + "] " + req.Text, "detectedSourceLanguage": "EN"})
	}))
	defer tr.Close()
	gr := httptest.NewServer(cors(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Prompt string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		_, text, _ := strings.Cut(req.Prompt, "\nText:\n")
		i := strings.Index(text, "canvas")
		answer := "[]"
		if i >= 0 {
			b, _ := json.Marshal([]map[string]any{{"offset": utf8.RuneCountInString(text[:i]), "length": 6, "message": "plural", "suggestion": "canvases"}})
			answer = string(b)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"response": answer})
	}))
	defer gr.Close()
	d := e2etest.NewSite(t).Open(t, "?translate="+url.QueryEscape(tr.URL)+"&grammar="+url.QueryEscape(gr.URL)+"&model=m1")
	d.ToggleMode()
	d.Click(`button[data-tab="language"]`)
	d.OK(strings.Contains(d.Text(".gj-lang-detected"), "Language: English"), "the language is detected: "+d.Text(".gj-lang-detected"))
	d.OK(d.Count(".gj-lang-target") == 1 && d.Count(".gj-lang-check") == 1, "both backends are offered")
	d.OK(d.Is("s.language.translate && s.language.grammar"), "the page knows both are set up")

	// Translate a selected word.
	d.OK(d.Disabled(".gj-lang-translate"), "translate waits for a selection")
	d.CanvasDblClick(240, 164)
	d.Click(`button[data-tab="stats"]`)
	d.Click(`button[data-tab="language"]`)
	d.Select(".gj-lang-target", "FR")
	d.Click(".gj-lang-translate")
	translated := d.Text(".gj-lang-translation")
	d.OK(strings.Contains(translated, "[FR]"), "the selection is translated by the server: "+translated)
	// Edited since it was translated: the range is not that text any more, so the
	// translation is refused, not put in the wrong place.
	stale := d.State().Str("text")
	d.Focus("#input")
	d.Press("Home")
	d.Type("Z")
	d.Wait("s.text !== " + e2etest.J(stale))
	d.Click(`button[data-tab="stats"]`)
	d.Click(`button[data-tab="language"]`)
	d.OK(d.Count(".gj-lang-use") == 1, "the translation is still on offer")
	edited := d.State().Str("text")
	d.Click(".gj-lang-use")
	d.WaitFor("the refusal", `(document.querySelector('.gj-doc-error, .gj-lang-error') || {textContent: ''}).textContent.includes('changed since')`)
	d.OK(d.State().Str("text") == edited, "and the text is left alone")
	d.CanvasDblClick(240, 164) // select a word again and translate it, now in the text as it is
	d.Click(`button[data-tab="stats"]`)
	d.Click(`button[data-tab="language"]`)
	d.Click(".gj-lang-translate")
	d.WaitFor("a new translation", "document.querySelector('.gj-lang-translation')")
	before := d.State().Str("text")
	d.Click(".gj-lang-use")
	d.Wait("s.text !== " + e2etest.J(before))
	d.OK(strings.Contains(d.State().Str("text"), "[FR]"), "Replace the selection puts the translation in the text")

	// Grammar: the fake model flags the first "canvas" and offers "canvases".
	d.Click(".gj-lang-check")
	d.WaitFor("an issue", "document.querySelector('.gj-lang-issue')")
	d.OK(d.State().Int("language.issues") == 1, "an issue is found")
	label := d.Text(".gj-lang-apply")
	d.OK(strings.Contains(label, "canvases"), "with the fix on offer: "+label)
	d.Click(".gj-lang-apply")
	d.Wait("s.text.includes('canvases')")
	d.OK(d.Count(".gj-lang-issue") == 0, "applying it changes the text and clears the issue")
	d.NoErrors()
}
