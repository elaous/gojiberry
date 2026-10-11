//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Display preferences kept in the browser are applied at start: text size, spacing, contrast and motion
// change the page's computed styles, and none set leaves the defaults.
func TestDisplayPreferencesAreApplied(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	size := func() float64 { var f float64; d.Eval(`parseFloat(getComputedStyle(document.documentElement).fontSize)`, &f); return f }
	base := size()
	d.OK(!d.Bool(`document.documentElement.hasAttribute('data-gj-text')`), "no preference: no attribute")

	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({text: 'large', spacing: 'wide', contrast: 'more', motion: 'reduce'}))`, nil)
	d.Reload()
	d.WaitSelector("canvas")
	d.OK(size() == base*1.25, "text size is 125%")
	d.OK(d.Str(`getComputedStyle(document.body).letterSpacing`) != "normal", "wide spacing sets the letter spacing")
	d.OK(d.Str(`getComputedStyle(document.documentElement).getPropertyValue('--gj-text').trim()`) == "#000000", "more contrast darkens the text")
	d.OK(d.Str(`getComputedStyle(document.documentElement).getPropertyValue('--gj-border').trim()`) == "#4b5563", "and the borders")

	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({text: 'bogus', contrast: 'x'}))`, nil)
	d.Reload()
	d.WaitSelector("canvas")
	d.OK(size() == base && !d.Bool(`document.documentElement.hasAttribute('data-gj-text') || document.documentElement.hasAttribute('data-gj-contrast')`), "unknown values are ignored")
	d.NoErrors()
}
