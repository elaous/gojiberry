//go:build js && wasm && !experimental && exp_forms && !exp_forms_submit

package main

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

// Without the exp_forms_submit tag nothing offers to send answers anywhere.
func submitNodes(*ui, *canvas.TextFlow, func()) []xango.Node { return nil }
