//go:build js && wasm && !experimental && !exp_map

package main

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

type mapHolder struct{}

func mapSection(u *ui, _ *State, _ func() *canvas.TextFlow, _, _ func()) []xango.Node {
	u.mapHolder = mapHolder{}
	return nil
}
