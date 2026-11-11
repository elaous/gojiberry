//go:build js && wasm

package main

import (
	"slices"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/i18n"
)

var lazyLanguages = map[string]string{"pt": "Português", "ar": "العربية", "he": "עברית", "en-x-plain": "English, plain words"}

var onLocale func()

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
