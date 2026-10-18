//go:build js && wasm && !experimental && !exp_map

package main

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

// Maps from OpenStreetMap are an experiment (build tag exp_map, or experimental): without it the Pictures tab has no
// map form and nothing asks an OpenStreetMap server for anything.

type mapHolder struct{}

func mapSection(u *ui, _ *State, _ func() *canvas.TextFlow, _, _ func()) []xango.Node {
	u.mapHolder = mapHolder{}
	return nil
}
