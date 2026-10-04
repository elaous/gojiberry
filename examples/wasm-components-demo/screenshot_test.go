//go:build !(js && wasm)

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestScreenshot loads the built demo in headless Chromium and saves a
// full-page screenshot, to verify that it actually mounts and renders (not just
// that it builds). It only runs when OUT_FILE names where to write the PNG
// (`make screenshot` sets it).
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
