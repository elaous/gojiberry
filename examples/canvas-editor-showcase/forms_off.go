//go:build js && wasm && !experimental && !exp_forms

package main

import (
	"slices"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/xango"
)

type formsHolder struct{}

func init() { sideTabs = slices.DeleteFunc(sideTabs, func(t string) bool { return t == "forms" }) }

func formsTab(*ui, string, func()) xango.Node { return xango.Text("") }

func formsMenuItem(*ui, func() *canvas.TextFlow, func(), func()) components.MenuItem {
	return components.MenuItem{}
}

func (u *ui) wireForms(*canvas.TextFlow, func(), func()) { u.formsHolder = formsHolder{} }

func formsPanel(*ui, *State, func() *canvas.TextFlow, func(), func()) xango.Node { return xango.Frag() }
