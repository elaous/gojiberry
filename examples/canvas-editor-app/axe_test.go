//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestNoAccessibilityViolationsAxe(t *testing.T) {
	d := e2etest.NewSite(t).OpenURL(t, "/index.html", e2etest.Options{Width: 900, Height: 700, ConsoleErrors: true})
	d.Sleep(800)
	for _, v := range d.Axe("") {
		t.Errorf("the app: %s", v)
	}
}
