//go:build !(js && wasm) && !experimental && !exp_map

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestMapsAreNotInADefaultBuild(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#imgfind-query")
	d.OK(!d.Has("#map-query"), "no map form")
	d.NoErrors()
}
