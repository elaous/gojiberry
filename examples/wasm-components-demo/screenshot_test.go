//go:build !(js && wasm)

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestScreenshot(t *testing.T) {
	out := os.Getenv("OUT_FILE")
	if out == "" {
		t.Skip("set OUT_FILE to take a screenshot")
	}
	d := e2etest.NewSite(t, "../../components/*.css").OpenURL(t, "/index.html", e2etest.Options{Width: 1400, Height: 1000, ConsoleErrors: true})
	d.Sleep(1500)
	d.Screenshot(out)
	if errs := d.Errors(); len(errs) > 0 {
		t.Errorf("console errors: %s", strings.Join(errs, "; "))
	}
}
