//go:build !(js && wasm)

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	e2etest.RunEnv(t, "e2e/lang.js", "TRANSLATE_URL="+tr.URL, "GRAMMAR_URL="+gr.URL)
}
