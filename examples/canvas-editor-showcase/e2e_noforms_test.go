//go:build !(js && wasm) && !experimental && !exp_forms

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Form fields are an experiment: a build without the exp_forms tag has no Forms tab (and the arrow keys do not stop
// on one).
func TestFormsAreNotInADefaultBuild(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.OK(!d.Has(`button[data-tab="forms"]`), "no Forms tab")
	d.NoErrors()
}
