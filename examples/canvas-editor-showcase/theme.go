//go:build js && wasm

package main

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/tokens"
	"github.com/elaous/gojiberry/xango"
)

// themeState is the document's theme while it is edited. Until the person changes something the document
// has no theme and exports are as they were; from then on a theme without problems goes with the document
// (in its metadata) and into the HTML export.
type themeState struct {
	set tokens.Set
	on  bool
}

// current is the set being edited (the default one before any edit).
func (t *themeState) current() tokens.Set {
	if t.set.Colors == nil {
		t.set = tokens.Default()
	}
	return t.set
}

// themeInto writes the theme into d when there is one and it can be read comfortably.
func (u *ui) themeInto(d *docfile.Document) {
	if !u.theme.on {
		d.SetTheme("")
		return
	}
	if css, err := u.theme.set.CSS(); err == nil {
		d.SetTheme(css)
	}
}

// canvasTheme is what the sheets are drawn in: the theme when there is one that can be read comfortably, else
// nothing (the canvas's own colours).
func (u *ui) canvasTheme() *canvas.FlowTheme {
	if !u.theme.on || len(u.theme.set.Validate()) > 0 {
		return nil
	}
	c := u.theme.set.Colors
	return &canvas.FlowTheme{Text: c[tokens.Text], Background: c[tokens.Background], Link: c[tokens.Link]}
}

// loadTheme takes the theme of an opened document (none: the default, switched off).
func (u *ui) loadTheme(d docfile.Document) {
	u.theme = themeState{}
	if css := d.Theme(); css != "" {
		if set, ok := tokens.FromCSS(css); ok {
			u.theme = themeState{set: set, on: true}
		}
	}
}

// themePanel is the theme editor of the Styles tab.
func themePanel(u *ui, state *State, renderCanvas, renderUI func()) xango.Node {
	apply := func() {
		state.Theme = u.canvasTheme()
		renderCanvas()
		renderUI()
	}
	return xango.Frag(components.ThemeEditor(components.ThemeEditorProps{
		Set: u.theme.current(),
		OnChange: func(s tokens.Set) {
			u.theme.set, u.theme.on = s, true
			apply()
		},
		OnReset: func() {
			u.theme = themeState{}
			apply()
		},
	}), u.themeLibrary(func(s tokens.Set) {
		u.theme.set, u.theme.on = s, true
		apply()
	}, renderUI))
}
