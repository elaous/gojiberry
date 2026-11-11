//go:build !(js && wasm) && !experimental && !exp_forms

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestFormsAreNotInADefaultBuild(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.OK(!d.Has(`button[data-tab="forms"]`), "no Forms tab")
	d.NoErrors()
}
