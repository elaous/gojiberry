//go:build js && wasm

package main

import (
	"slices"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/i18n"
)

// lazyLanguages are the interface languages that are not in the program: their packs are files next to the
// page (lang-<code>.json), fetched the first time one is chosen, so a language costs nothing to a visitor who
// does not use it. They are drafts that need a native speaker's review (docs/translating.md).
var lazyLanguages = map[string]string{"pt": "Português", "ar": "العربية", "he": "עברית", "en-x-plain": "English, plain words"}

// onLocale, when set, is called after a language that had to be fetched has been put in force, for what has to be drawn again.
var onLocale func()

// langCodes are the interface languages on offer: English, the ones in the program, and the lazy ones.
func langCodes() []string {
	codes := []string{"en"}
	codes = append(codes, catalog.Languages()...)
	for c := range lazyLanguages {
		if !slices.Contains(codes, c) {
			codes = append(codes, c)
		}
	}
	slices.Sort(codes[1:])
	return codes
}

// useLocale puts the interface language for code in force: at once when the catalog has it, after fetching its
// pack when it is one of the lazy languages (the language stays as it was until then; a failed fetch leaves it).
func useLocale(code string) {
	best := i18n.Match([]string{code}, langCodes(), "en")
	if _, lazy := lazyLanguages[best]; lazy && !slices.Contains(catalog.Languages(), best) {
		go func() {
			if err := fetchLangPack(best); err != nil {
				js.Global().Get("console").Call("warn", "language pack "+best+": "+err.Error())
				return
			}
			setLocale(best)
			if onLocale != nil {
				onLocale()
			}
		}()
		return
	}
	setLocale(best)
}

// fetchLangPack loads lang-<code>.json into the catalog. It blocks: call it from a goroutine of its own.
func fetchLangPack(code string) error {
	resp, err := domrt.Await(js.Global().Call("fetch", "lang-"+code+".json"))
	if err != nil {
		return err
	}
	if !resp.Get("ok").Bool() {
		return &packError{code, resp.Get("status").Int()}
	}
	text, err := domrt.Await(resp.Call("text"))
	if err != nil {
		return err
	}
	return catalog.AddJSON([]byte(text.String()))
}

type packError struct {
	code   string
	status int
}

func (e *packError) Error() string {
	return "lang-" + e.code + ".json answered " + strings.TrimSpace(js.Global().Get("String").Invoke(e.status).String())
}
