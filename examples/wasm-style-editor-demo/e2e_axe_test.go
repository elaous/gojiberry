//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// The demo is under the accessibility gate: axe finds nothing wrong with it, with the font selector closed and
// open.
func TestNoAccessibilityViolationsAxe(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "", e2etest.Options{Width: 900, Height: 900})
	for _, v := range d.Axe("") {
		t.Errorf("the demo: %s", v)
	}
	d.Click(".gj-fontselector-control")
	d.WaitSelector(".gj-fontselector-panel")
	for _, v := range d.Axe("") {
		t.Errorf("with the font selector open: %s", v)
	}
}
