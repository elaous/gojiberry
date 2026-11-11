//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/e2etest"
)

func TestNoAccessibilityViolationsAxe(t *testing.T) {
	d := e2etest.NewSite(t, "../../components/*.css").OpenURL(t, "/index.html", e2etest.Options{Width: 1400, Height: 1000, ConsoleErrors: true})
	d.Sleep(1000)
	for _, v := range d.Axe("") {
		t.Errorf("the gallery: %s", v)
	}
}
