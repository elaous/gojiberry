//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestLazyLanguagesAndRightToLeft(t *testing.T) {
	site := e2etest.NewSite(t)
	for _, c := range []struct{ lang, dir, find string }{
		{"pt", "ltr", "Localizar"},
		{"ar", "rtl", "بحث"},
		{"he", "rtl", "חיפוש"},
	} {
		d := site.Open(t, "?lang="+c.lang)
		d.WaitFor("the page language", `document.documentElement.lang === '`+c.lang+`'`)
		d.OK(d.Str(`document.documentElement.dir`) == c.dir, c.lang+": direction "+d.Str(`document.documentElement.dir`))
		d.WaitFor("the Find tab in "+c.lang, `[...document.querySelectorAll('button[data-tab="find"]')].some(b => b.textContent.includes('`+c.find+`'))`)
		d.NoErrors()
	}
	d := site.Open(t, "?lang=en")
	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({lang: 'he'}))`, nil)
	d.Goto(site, "/?x=1") // no ?lang=: the language the person chose
	d.WaitFor("the chosen language", `document.documentElement.lang === 'he'`)
	d.OK(strings.Contains(d.Text(`button[data-tab="find"]`), "חיפוש"), "the tab is in Hebrew: "+d.Text(`button[data-tab="find"]`))
}

func TestRightToLeftInterfacePassesAxe(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=ar")
	d.WaitFor("Arabic", `document.documentElement.lang === 'ar' && [...document.querySelectorAll('button[data-tab="find"]')].some(b => b.textContent.includes('بحث'))`)
	for _, v := range d.Axe("", axeDisabled...) {
		t.Errorf("Arabic: %s", v)
	}
}
