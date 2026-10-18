//go:build js && wasm && !experimental && !exp_forms

package main

import (
	"slices"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/xango"
)

// Form fields are an experiment (build tag exp_forms, or experimental): without it the page has no Forms tab, no
// Insert > Form fields, and nothing draws or fills a field. A document that holds fields still keeps them, so saving it
// from a build with the tag loses nothing.

type formsHolder struct{}

func init() { sideTabs = slices.DeleteFunc(sideTabs, func(t string) bool { return t == "forms" }) }

// An empty text, not an empty Frag: a Frag with nothing in it upset the diff of the row of tabs and the focus on a tab was lost.
func formsTab(*ui, string, func()) xango.Node { return xango.Text("") }

// formsMenuItem is an item with nothing in it: withoutBlank drops it.
func formsMenuItem(*ui, func() *canvas.TextFlow, func(), func()) components.MenuItem {
	return components.MenuItem{}
}

func (u *ui) wireForms(*canvas.TextFlow, func(), func()) { u.formsHolder = formsHolder{} }

func formsPanel(*ui, *State, func() *canvas.TextFlow, func(), func()) xango.Node { return xango.Frag() }
