//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestPlainEnglishVariantIsFetchedAndKeepsThePageInEnglish(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en-x-plain")
	d.WaitFor("the language is in force", `document.documentElement.getAttribute('lang') === 'en-x-plain'`)
	d.Click(`button[data-tab="people"]`)
	d.WaitFor("the plain wording", `document.body.innerText.includes('What locking protects') || document.body.innerText.includes('Turn off')`)
	d.OK(d.Str(`document.documentElement.getAttribute('dir')`) != "rtl", "left to right")
	d.NoErrors()
}
