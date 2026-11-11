//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestSymbolPickerArrowKeys(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.Click(`button[data-tab="symbols"]`)
	d.WaitSelector(".gj-symbol")
	focused := func() string { return d.Str(`(document.activeElement.dataset || {}).char || ''`) }
	top := func() int {
		return d.Int(`Math.round(document.activeElement.getBoundingClientRect().top)`)
	}
	chars := d.Texts(".gj-symbol")
	if len(chars) < 30 {
		t.Fatalf("%d symbols", len(chars))
	}
	d.Eval(`document.querySelector('.gj-symbol').focus()`, nil)
	d.OK(focused() == chars[0], "the first symbol has the focus")
	d.Press("ArrowRight")
	d.OK(focused() == chars[1], "right goes to the next symbol: "+focused())
	d.Press("ArrowLeft")
	d.OK(focused() == chars[0], "left goes back")
	row0 := top()
	d.Press("ArrowDown")
	d.OK(top() > row0 && focused() != chars[0], "down goes to the next row")
	d.Press("ArrowUp")
	d.OK(focused() == chars[0], "up goes back to the same symbol")
	d.Press("End")
	d.OK(focused() == chars[len(chars)-1], "End goes to the last symbol")
	d.Press("Home")
	d.OK(focused() == chars[0], "Home goes to the first")
	d.Press("Tab")
	d.OK(!d.Bool(`document.activeElement.classList.contains('gj-symbol')`), "Tab leaves the grid after one stop")
	d.NoErrors()
}
